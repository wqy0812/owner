package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type workflowStore interface {
	CreateWorkflowSession(context.Context, domain.WorkflowSession) (domain.WorkflowSession, error)
	GetWorkflowSession(context.Context, string) (domain.WorkflowSession, error)
	ListWorkflowSessions(context.Context, string, string) ([]domain.WorkflowSession, error)
	UpdateWorkflowSession(context.Context, domain.WorkflowSession, int64) (domain.WorkflowSession, error)
	InterruptPreparations(context.Context) error
	GetEnvironment(context.Context, string, bool) (domain.Environment, error)
	GetComponent(context.Context, string, bool) (domain.Component, error)
	GetComponentRelease(context.Context, string) (domain.ComponentRelease, error)
	GetScenario(context.Context, string, bool) (domain.Scenario, error)
	GetScenarioRevision(context.Context, string) (domain.ScenarioRevision, error)
	GetUser(context.Context, string) (domain.User, error)
}
type PreparationService struct {
	store        workflowStore
	execution    *ExecutionService
	environments *EnvironmentService
	runtime      RuntimeInspector
	hub          *EventHub
	root         context.Context
	mu           sync.Mutex
	active       map[string]context.CancelFunc
	workers      chan struct{}
}
type PreparationInput struct {
	Kind           string                   `json:"kind"`
	SubjectID      string                   `json:"subjectId"`
	EnvironmentID  string                   `json:"environmentId"`
	IdempotencyKey string                   `json:"idempotencyKey"`
	Component      ComponentTestRequest     `json:"component"`
	Scenario       ScenarioExecutionRequest `json:"scenario"`
}
type PreparationCheck struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	Label     string    `json:"label"`
	Host      string    `json:"host,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Source    string    `json:"source,omitempty"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	ElapsedMS int64     `json:"elapsedMs"`
}
type PreparationResult struct {
	Checks      []PreparationCheck      `json:"checks"`
	Plan        any                     `json:"plan,omitempty"`
	Error       string                  `json:"error,omitempty"`
	Explanation *domain.WorkExplanation `json:"explanation,omitempty"`
}
type ExecutorHealth struct {
	CheckID        string    `json:"checkId"`
	Status         string    `json:"status"`
	Controller     string    `json:"controller"`
	Ansible        string    `json:"ansible"`
	Python         string    `json:"python"`
	CheckedAt      time.Time `json:"checkedAt"`
	Message        string    `json:"message"`
	RepairLocation string    `json:"repairLocation"`
}

func (s *PreparationService) Health(ctx context.Context) ExecutorHealth {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hostname, _ := os.Hostname()
	result := ExecutorHealth{CheckID: newID("executor-check"), Status: "passed", Controller: hostname, CheckedAt: time.Now().UTC(), RepairLocation: "平台控制机的 Ansible 可执行文件、Python 解释器及服务环境配置"}
	runtime, err := s.runtime.RuntimeIdentity(ctx)
	if err == nil {
		err = s.runtime.CheckRuntime(ctx)
	}
	if err != nil {
		result.Status = "failed"
		result.Message = safePreparationError(err)
		log.Printf("executor health check %s failed: %s", result.CheckID, result.Message)
	} else {
		result.Ansible = runtime.AnsibleCore
		result.Python = runtime.Python
		result.Message = "运行时与必要动作、回调插件可加载；组件脚本在作业编译时继续校验"
	}
	return result
}
func (s *PreparationService) Recover(ctx context.Context) error {
	return s.store.InterruptPreparations(ctx)
}
func (s *PreparationService) Get(ctx context.Context, user domain.User, id string) (domain.WorkflowSession, error) {
	v, e := s.store.GetWorkflowSession(ctx, id)
	if e != nil {
		return v, e
	}
	if v.Kind != "preparation" || v.OwnerID != user.ID {
		return domain.WorkflowSession{}, domain.ErrForbidden
	}
	return v, nil
}
func (s *PreparationService) List(ctx context.Context, user domain.User) ([]domain.WorkflowSession, error) {
	all, e := s.store.ListWorkflowSessions(ctx, "preparation", user.ID)
	out := []domain.WorkflowSession{}
	for _, v := range all {
		if v.OwnerID == user.ID {
			out = append(out, v)
		}
	}
	return out, e
}
func (s *PreparationService) Create(ctx context.Context, user domain.User, input PreparationInput) (domain.WorkflowSession, error) {
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return domain.WorkflowSession{}, fmt.Errorf("%w: preparation request key is required", domain.ErrInvalid)
	}
	environment, e := s.store.GetEnvironment(ctx, input.EnvironmentID, false)
	if e != nil {
		return domain.WorkflowSession{}, e
	}
	switch input.Kind {
	case "component_test":
		release, e := s.store.GetComponentRelease(ctx, input.SubjectID)
		if e != nil {
			return domain.WorkflowSession{}, e
		}
		component, e := s.store.GetComponent(ctx, release.ComponentID, false)
		if e != nil {
			return domain.WorkflowSession{}, e
		}
		if user.Role != domain.RoleComponentOwner || component.OwnerID != user.ID {
			return domain.WorkflowSession{}, domain.ErrForbidden
		}

	case "scenario_execution":
		revision, e := s.store.GetScenarioRevision(ctx, input.SubjectID)
		if e != nil {
			return domain.WorkflowSession{}, e
		}
		scenario, e := s.store.GetScenario(ctx, revision.ScenarioID, false)
		if e != nil {
			return domain.WorkflowSession{}, e
		}
		if !(user.Role == domain.RoleScenarioOwner && scenario.OwnerID == user.ID) && !(user.Role == domain.RoleEnvironmentOwner && environment.OwnerID == user.ID) {
			return domain.WorkflowSession{}, domain.ErrForbidden
		}
	default:
		return domain.WorkflowSession{}, fmt.Errorf("%w: unsupported preparation kind", domain.ErrInvalid)
	}
	input.Component.EnvironmentID, input.Scenario.EnvironmentID = input.EnvironmentID, input.EnvironmentID
	raw, _ := json.Marshal(input)
	output, _ := json.Marshal(PreparationResult{Checks: []PreparationCheck{}})
	now := time.Now().UTC()
	id := newID("preparation")
	v, e := s.store.CreateWorkflowSession(ctx, domain.WorkflowSession{ID: id, Kind: "preparation", OwnerID: user.ID, RequestKey: input.IdempotencyKey, RequestDigest: digestValue(input), Status: "queued", Input: raw, Output: output, CreatedAt: now, UpdatedAt: now})
	if e != nil {
		return v, e
	}
	if v.ID != id {
		return v, nil
	}
	work, cancel := context.WithTimeout(s.root, 10*time.Minute)
	s.mu.Lock()
	s.active[v.ID] = cancel
	s.mu.Unlock()
	go s.run(work, cancel, user, v, input)
	return v, nil
}
func (s *PreparationService) Cancel(ctx context.Context, user domain.User, id string) (domain.WorkflowSession, error) {
	v, e := s.Get(ctx, user, id)
	if e != nil {
		return v, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.active[id]; cancel != nil {
		cancel()
	}
	// The worker persists cancellation after stopping its probes.
	return v, nil
}
func (s *PreparationService) run(ctx context.Context, cancel context.CancelFunc, user domain.User, v domain.WorkflowSession, input PreparationInput) {
	defer cancel()
	defer func() { s.mu.Lock(); delete(s.active, v.ID); s.mu.Unlock() }()
	result := PreparationResult{Checks: []PreparationCheck{}}
	var reportMu sync.Mutex
	persist := func(status string) {
		if ctx.Err() != nil && (status == "failed" || status == "succeeded" || status == "cancelled") {
			status = "cancelled"
			if ctx.Err() == context.DeadlineExceeded {
				status = "timed_out"
			}
		}
		v.Status = status
		v.Output, _ = json.Marshal(result)
		saveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		next, e := s.store.UpdateWorkflowSession(saveCtx, v, v.Version)
		if e != nil {
			log.Printf("preparation %s persistence failed: %v", v.ID, e)
			cancel()
			return
		}
		v = next
		s.hub.Publish("preparation.updated", map[string]any{"preparationId": v.ID})
	}
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	case <-ctx.Done():
		result.Error = "准备已取消"
		persist("cancelled")
		return
	}
	persist("running")
	report := func(check PreparationCheck) {
		reportMu.Lock()
		defer reportMu.Unlock()
		for i, item := range result.Checks {
			if item.ID == check.ID {
				result.Checks[i] = check
				persist("running")
				return
			}
		}
		result.Checks = append(result.Checks, check)
		persist("running")
	}
	health := s.Health(ctx)
	report(PreparationCheck{ID: "runtime", Category: "runtime", Label: "执行器 · " + health.Controller, Status: health.Status, Message: health.Message + "；检查编号：" + health.CheckID + " · " + health.Ansible + " / Python " + health.Python + "；修复位置：" + health.RepairLocation, StartedAt: health.CheckedAt})
	if health.Status != "passed" {
		result.Error = health.Message
		persist("failed")
		return
	}
	// Connectivity is a separate observation and never implies installability.
	connectivityStarted := time.Now().UTC()
	connectivity, e := s.environments.preparationConnectivity(ctx, user, input.EnvironmentID)
	if e != nil {
		report(PreparationCheck{ID: "connectivity", Category: "connectivity", Label: "环境连通性", Status: "failed", Message: safePreparationError(e), StartedAt: connectivityStarted})
	} else {
		for i, h := range connectivity.SSH.Results {
			report(PreparationCheck{ID: fmt.Sprintf("ssh-%d", i), Category: "connectivity", Label: "SSH", Host: h.Name, Status: h.Status, Message: h.Message, StartedAt: connectivityStarted})
		}
	}
	ctx = withMediaObservation(ctx, report)
	ctx = context.WithValue(ctx, preparationProbeKey{}, preparationProbeObserver(func(ctx context.Context, environment domain.Environment, plan lockedPlan) error {
		return s.environments.probeForPreparation(ctx, environment, plan, report)
	}))
	var plan any
	if input.Kind == "component_test" {
		plan, e = s.execution.PreviewComponentTest(ctx, user, input.SubjectID, input.Component)
	} else {
		var preview ScenarioExecutionPreview
		preview, e = s.execution.PreviewScenarioExecution(ctx, user, input.SubjectID, input.Scenario, preparationRunKind(input.Scenario))
		if e == nil && !preview.Ready {
			messages := []string{"场景执行计划被阻断"}
			for _, issue := range preview.Issues {
				messages = append(messages, issue.Message)
			}
			e = fmt.Errorf("%w: %s", domain.ErrConflict, strings.Join(messages, "；"))
		}
		plan = preview
	}
	if e != nil {
		result.Error = safePreparationError(e)
		if ae, ok := e.(*domain.ActionableError); ok {
			result.Explanation = &ae.Explanation
		}
		status := "failed"
		if ctx.Err() != nil {
			status = "cancelled"
			if ctx.Err() == context.DeadlineExceeded {
				status = "timed_out"
			}
		}
		persist(status)
		return
	}
	if ctx.Err() != nil {
		result.Error = "准备已取消"
		persist("cancelled")
		return
	}
	for _, check := range result.Checks {
		if check.Status == "failed" {
			result.Error = "执行准备存在失败检查，请处理后重新准备"
			persist("failed")
			return
		}
	}
	result.Plan = plan
	persist("succeeded")
}
func safePreparationError(err error) string {
	text := err.Error()
	text = regexp.MustCompile(`https?://[^\s"'<>]+`).ReplaceAllStringFunc(text, func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil {
			return "[invalid URL]"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	})
	if len(text) > 1500 {
		text = text[:1500]
	}
	return text
}

func preparationRunKind(input ScenarioExecutionRequest) domain.RunKind {
	if input.TestOnly {
		return domain.RunScenarioTest
	}
	return domain.RunScenario
}

func (s *EnvironmentService) preparationConnectivity(ctx context.Context, user domain.User, id string) (domain.EnvironmentConnectivityCheck, error) {
	environment, err := s.store.GetEnvironment(ctx, id, false)
	if err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	if err = ensureEnvironmentActive(environment); err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	if environment.Revision == nil {
		return domain.EnvironmentConnectivityCheck{}, fmt.Errorf("%w: environment revision is required", domain.ErrConflict)
	}
	tcp, err := s.checkEnvironmentHealth(ctx, user, environment)
	if err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	ssh, err := s.checkEnvironmentSSH(ctx, user, environment)
	return domain.EnvironmentConnectivityCheck{TCP: tcp, SSH: ssh}, err
}
