package ansible

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type JobEvent struct {
	Message   string         `json:"message,omitempty"`
	OwnerTask bool           `json:"ownerTask,omitempty"`
	Kind      string         `json:"kind"`
	StepID    string         `json:"stepId"`
	Sequence  int            `json:"sequence,omitempty"`
	Host      string         `json:"host,omitempty"`
	Task      string         `json:"task,omitempty"`
	Status    string         `json:"status,omitempty"`
	Changed   bool           `json:"changed,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Token     string         `json:"token,omitempty"`
}

type JobStepResult struct {
	StepID     string               `json:"stepId"`
	Status     string               `json:"status"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt time.Time            `json:"finishedAt"`
	Error      string               `json:"error,omitempty"`
	Hosts      map[string]HostRecap `json:"hosts"`
}

type JobRequest struct {
	Plan                JobPlan
	Credentials         map[string]any
	ConnectionVariables map[string]any
	SecretValues        []string
	LogSink             LogSink
	OnEvent             func(JobEvent) error
	OnBoundary          func(context.Context, string, JobStep, JobStepResult) error
	// StagePreparationTimeout bounds synchronous media preparation before an
	// action starts. Zero uses the action timeout; action time starts after ACK.
	StagePreparationTimeout time.Duration
}

type JobResult struct {
	Result
	ExitCode *int            `json:"exitCode,omitempty"`
	Steps    []JobStepResult `json:"steps"`
}

func (r *Runner) RunJob(ctx context.Context, request JobRequest) (JobResult, error) {
	bundle, err := r.BuildJob(ctx, request.Plan)
	if err != nil {
		return JobResult{}, err
	}
	defer bundle.Close()
	return r.RunBundle(ctx, bundle, request)
}

type jobController struct {
	initialized   bool
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	bundle        *JobBundle
	request       JobRequest
	token         string
	active        int
	next          int
	sequence      int
	playSeen      bool
	ownerObserved map[string]bool
	results       []JobStepResult
	failure       error
	redactor      *Redactor
	deadline      atomic.Int64
	finished      atomic.Bool
	processGroup  atomic.Int64
}

func (c *jobController) fail(err error) error {
	if c.failure == nil {
		c.failure = err
	}
	c.cancel()
	return err
}
func (c *jobController) handle(event JobEvent) error {
	if event.Token != c.token {
		return fmt.Errorf("invalid controller token")
	}
	// Heartbeats must remain responsive during durable writes or media
	// preparation; the independent deadline still bounds those operations.
	if event.Kind == "heartbeat" {
		return c.ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return c.failure
	}
	if event.Kind == "abort" {
		return c.fail(fmt.Errorf("job preflight: %s", event.Message))
	}
	if event.Kind == "initialize" {
		if c.initialized || c.next != 0 || c.active >= 0 {
			return c.fail(fmt.Errorf("job already initialized"))
		}
		c.initialized = true
		return nil
	}
	if event.Kind == "finish" {
		if !c.finished.Load() {
			return c.fail(fmt.Errorf("job ended before all selected stages completed"))
		}
		return nil
	}
	if event.Kind == "current" {
		return nil
	}
	steps := c.bundle.Manifest.Plan.Steps
	if event.Kind == "begin" || event.Kind == "end" {
		if c.active < 0 && (c.next >= len(steps) || event.StepID != steps[c.next].ID) {
			for _, recovery := range c.bundle.Manifest.Plan.Recovery {
				if event.StepID == recovery.ID {
					return nil
				}
			}
		}
	}
	if c.next >= len(steps) {
		return c.fail(fmt.Errorf("unexpected event after final stage"))
	}
	if event.StepID != steps[c.next].ID {
		return c.fail(fmt.Errorf("out-of-order stage event"))
	}
	step := steps[c.next]
	switch event.Kind {
	case "begin":
		if c.active >= 0 {
			return c.fail(fmt.Errorf("stage already active"))
		}
		if err := c.bundle.Validate(); err != nil {
			return c.fail(err)
		}
		result := JobStepResult{StepID: step.ID, Status: "running", StartedAt: time.Now().UTC(), Hosts: map[string]HostRecap{}}
		c.results = append(c.results, result)
		c.active = c.next
		preparationTimeout := c.request.StagePreparationTimeout
		if preparationTimeout <= 0 {
			preparationTimeout = time.Duration(step.TimeoutSeconds) * time.Second
		}
		c.deadline.Store(time.Now().Add(preparationTimeout).UnixNano())
		c.playSeen = false
		c.ownerObserved = map[string]bool{}
		if c.request.OnBoundary != nil {
			if err := c.request.OnBoundary(c.ctx, "begin", step, result); err != nil {
				return c.fail(err)
			}
		}
		if err := c.ctx.Err(); err != nil {
			return c.fail(err)
		}
		c.deadline.Store(time.Now().Add(time.Duration(step.TimeoutSeconds) * time.Second).UnixNano())
	case "end":
		if c.active != c.next || !c.playSeen {
			return c.fail(fmt.Errorf("missing stage observations"))
		}
		result := c.results[c.active]
		if len(result.Hosts) == 0 {
			return c.fail(fmt.Errorf("stage %s matched no target hosts", step.ID))
		}
		for host, recap := range result.Hosts {
			if (step.Action == "check" || step.SourceType == "scenario_acceptance") && !c.ownerObserved[host] {
				return c.fail(fmt.Errorf("check did not execute on host %s", host))
			}
			if recap.Failed > 0 || recap.Unreachable > 0 {
				return c.fail(fmt.Errorf("stage %s failed on a target", step.ID))
			}
		}
		if err := c.bundle.Validate(); err != nil {
			return c.fail(err)
		}
		result.Status = "succeeded"
		result.FinishedAt = time.Now().UTC()
		if c.request.OnBoundary != nil {
			if err := c.request.OnBoundary(c.ctx, "end", step, result); err != nil {
				return c.fail(err)
			}
		}
		c.results[c.active] = result
		c.active = -1
		c.deadline.Store(0)
		c.next++
		if c.next == len(steps) {
			c.finished.Store(true)
		}
	default:
		if c.active != c.next {
			return c.fail(fmt.Errorf("event outside active stage"))
		}
		if event.Sequence != c.sequence+1 {
			return c.fail(fmt.Errorf("missing or duplicated job event"))
		}
		c.sequence = event.Sequence
		if event.Kind == "play_start" {
			if c.playSeen {
				return c.fail(fmt.Errorf("duplicate stage play"))
			}
			c.playSeen = true
		}
		if event.Kind == "result" {
			result := &c.results[c.active]
			recap := result.Hosts[event.Host]
			switch event.Status {
			case "ok":
				if event.OwnerTask {
					c.ownerObserved[event.Host] = true
				}
				recap.OK++
				if event.Changed {
					recap.Changed++
				}
			case "failed":
				recap.Failed++
				result.Error = "task failed: " + event.Task
			case "unreachable":
				recap.Unreachable++
				result.Error = "host unreachable: " + event.Host
			case "skipped":
				recap.Skipped++
			default:
				return c.fail(fmt.Errorf("invalid task status"))
			}
			result.Hosts[event.Host] = recap
		}
		if event.Kind == "waiting" {
			if waiting, ok := event.Result["waiting"].(map[string]any); ok {
				waiting["deadline"] = time.Unix(0, c.deadline.Load()).UTC().Format(time.RFC3339Nano)
			}
		}
		if c.request.OnEvent != nil {
			event.Token = ""
			if c.redactor != nil {
				data, err := json.Marshal(event)
				if err != nil {
					return c.fail(err)
				}
				if err := json.Unmarshal([]byte(c.redactor.Redact(string(data))), &event); err != nil {
					return c.fail(fmt.Errorf("cannot redact structured event"))
				}
			}
			if err := c.request.OnEvent(event); err != nil {
				return c.fail(err)
			}
		}
	}
	return nil
}

func (r *Runner) RunBundle(parent context.Context, bundle *JobBundle, request JobRequest) (result JobResult, runErr error) {
	if err := bundle.Validate(); err != nil {
		return result, err
	}
	for _, name := range bundle.Manifest.Plan.RequiredCredentials {
		value, ok := request.Credentials[name]
		if !ok || value == nil || value == "" {
			return result, fmt.Errorf("missing credential %s", name)
		}
	}
	for name := range request.ConnectionVariables {
		if !strings.HasPrefix(name, "ansible_") {
			return result, fmt.Errorf("invalid connection variable %s", name)
		}
	}
	actual, err := r.RuntimeIdentity(parent)
	if err != nil {
		return result, err
	}
	if actual != bundle.Manifest.Plan.Runtime {
		return result, fmt.Errorf("Ansible/Python runtime differs from locked job")
	}
	// Run from a private copy. The export remains immutable and reusable.
	workspace, err := r.createWorkspace()
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(workspace)
	executionRoot := filepath.Join(workspace, "job")
	if err := copyRegularTree(bundle.Path, executionRoot); err != nil {
		return result, err
	}
	execution := &JobBundle{Path: executionRoot, Manifest: bundle.Manifest}
	inputDir := filepath.Join(workspace, "input")
	if err := os.MkdirAll(inputDir, 0700); err != nil {
		return result, err
	}
	extra := map[string]any{"cf_credentials": request.Credentials}
	for k, v := range request.ConnectionVariables {
		extra[k] = v
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return result, err
	}
	varsPath := filepath.Join(inputDir, "credentials.json")
	if err := writePrivateFile(varsPath, encoded); err != nil {
		return result, err
	}
	localTemp := filepath.Join(workspace, "tmp")
	if err := os.MkdirAll(localTemp, 0700); err != nil {
		return result, err
	}
	socketDir, err := os.MkdirTemp("", "cfj-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(socketDir)
	listener, err := net.Listen("unix", filepath.Join(socketDir, "control.sock"))
	if err != nil {
		return result, err
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	controller := &jobController{ctx: ctx, cancel: cancel, bundle: execution, request: request, token: newJobToken(), active: -1}
	var clients sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			clients.Add(1)
			go func() {
				defer clients.Done()
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(35 * time.Second))
				var event JobEvent
				err := json.NewDecoder(io.LimitReader(connection, 8<<20)).Decode(&event)
				if err == nil {
					err = controller.handle(event)
				}
				reply := map[string]any{"ok": err == nil, "done": controller.finished.Load(), "ansibleGroup": controller.processGroup.Load()}
				if err == nil {
					controller.mu.Lock()
					if event.Kind == "initialize" {
						reply["watchdog"] = true
					}
					if event.Kind == "begin" || event.Kind == "current" {
						reply["run"] = false
						if controller.active >= 0 && controller.next < len(execution.Manifest.Plan.Steps) {
							step := execution.Manifest.Plan.Steps[controller.next]
							if step.ID == event.StepID {
								reply["run"], reply["stepId"], reply["inputs"] = true, step.ID, step.Variables
								reply["context"] = map[string]any{"environmentId": execution.Manifest.Plan.EnvironmentID, "releaseId": step.ReleaseID, "componentId": step.ComponentID, "backupRef": step.Variables["clusterforge_backup_ref"], "stepId": step.ID, "nodeId": step.NodeID, "actionId": step.ActionID, "parentActionId": step.ParentActionID, "phase": step.Phase}
							}
						}
					}
					controller.mu.Unlock()
				}
				if err != nil {
					reply["error"] = err.Error()
				}
				_ = connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
				_ = json.NewEncoder(connection).Encode(reply)
			}()
		}
	}()
	defer func() { _ = listener.Close(); <-done; clients.Wait() }()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if deadline := controller.deadline.Load(); deadline > 0 && now.UnixNano() > deadline {
					cancel() // Do not wait for a blocked persistence callback to release its mutex.
					controller.mu.Lock()
					controller.fail(fmt.Errorf("stage timeout"))
					controller.mu.Unlock()
				}
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	runner := *r
	runner.onProcessStart = func(phase Phase, pid int) {
		if phase == PhaseExecute {
			controller.processGroup.Store(int64(pid))
		}
	}
	runner.Env = map[string]string{}
	for k, v := range r.Env {
		runner.Env[k] = v
	}
	runner.Env["ANSIBLE_CALLBACK_WHITELIST"] = "cf_events"
	runner.Env["CLUSTERFORGE_JOB_TRANSPORT"] = "platform"
	for k, v := range map[string]string{"ANSIBLE_CONFIG": filepath.Join(executionRoot, "ansible.cfg"), "ANSIBLE_ACTION_PLUGINS": filepath.Join(executionRoot, "action_plugins"), "ANSIBLE_CALLBACK_PLUGINS": filepath.Join(executionRoot, "callback_plugins"), "ANSIBLE_CALLBACKS_ENABLED": "cf_events", "ANSIBLE_ROLES_PATH": filepath.Join(executionRoot, "roles"), "ANSIBLE_STRATEGY": "linear", "ANSIBLE_FORCE_HANDLERS": "False", "ANSIBLE_CACHE_PLUGIN": "memory", "PYTHONDONTWRITEBYTECODE": "1", "CLUSTERFORGE_JOB_SOCKET": filepath.Join(socketDir, "control.sock"), "CLUSTERFORGE_JOB_TOKEN": controller.token} {
		runner.Env[k] = v
	}
	secrets := append(append([]string(nil), request.SecretValues...), controller.token)
	redactor := NewRedactor(secrets, extra, []byte(bundle.Manifest.Plan.Inventory))
	controller.redactor = redactor
	logs := newLogCollector(r.maxLogBytes(), request.LogSink, redactor)
	result.Result = Result{Playbook: "site.yml", StartedAt: time.Now().UTC(), TreeSHA256: bundle.Manifest.Digest, Recap: map[string]HostRecap{}}
	defer func() {
		controller.mu.Lock()
		defer controller.mu.Unlock()
		if controller.failure != nil {
			runErr = controller.failure
		}
		if runErr == nil && controller.next != len(execution.Manifest.Plan.Steps) {
			runErr = fmt.Errorf("job ended without acknowledging all stage results")
		}
		if controller.active >= 0 {
			current := &controller.results[controller.active]
			current.Status = "failed"
			current.FinishedAt = time.Now().UTC()
			if runErr != nil {
				current.Error = runErr.Error()
			}
			if errors.Is(parent.Err(), context.Canceled) {
				current.Status = "cancelled"
			}
		}
		result.Steps = append([]JobStepResult(nil), controller.results...)
		for i := range result.Steps {
			result.Steps[i].Error = redactor.Redact(result.Steps[i].Error)
		}
		if runErr != nil {
			runErr = fmt.Errorf("%s", redactor.Redact(runErr.Error()))
		}
		result.FinishedAt = time.Now().UTC()
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
		result.Successful = runErr == nil
		result.Canceled = errors.Is(parent.Err(), context.Canceled)
		result.TimedOut = runErr != nil && strings.Contains(runErr.Error(), "timeout")
		result.Logs, result.LogsTruncated = logs.Snapshot()
		result.Recap = logs.Recap()
	}()
	for _, phase := range []struct {
		kind Phase
		flag string
	}{{PhaseSyntaxCheck, "--syntax-check"}, {PhaseListHosts, "--list-hosts"}, {PhaseExecute, ""}} {
		args := []string{"-i", filepath.Join(executionRoot, "inventory.ini"), "--extra-vars", "@" + varsPath}
		if phase.flag != "" {
			args = append(args, phase.flag)
		}
		entry := "site.yml"
		if phase.kind == PhaseSyntaxCheck {
			if _, ok := bundle.Manifest.Files["syntax.yml"]; ok {
				entry = "syntax.yml"
			}
		}
		args = append(args, filepath.Join(executionRoot, entry))
		phaseResult, err := runner.runPhase(ctx, phase.kind, args, executionRoot, localTemp, logs)
		result.Phases = append(result.Phases, phaseResult)
		if phase.kind == PhaseExecute {
			code := phaseResult.ExitCode
			result.ExitCode = &code
		}
		if err != nil {
			return result, &PhaseError{Phase: phase.kind, ExitCode: phaseResult.ExitCode, Cause: err}
		}
		if phase.kind == PhaseListHosts {
			events, _ := logs.Snapshot()
			stages := len(bundle.Manifest.Plan.Steps)
			if IsNativeJobContract(bundle.Manifest.Contract) {
				stages += len(bundle.Manifest.Plan.Recovery)
			}
			if IsNativeJobContract(bundle.Manifest.Contract) {
				// Initialization and completion are additional controller plays.
				trimmed := make([]LogEvent, 0, len(events))
				hostLine := regexp.MustCompile(`^\s*hosts \(([0-9]+)\):`)
				seen := 0
				for _, event := range events {
					if event.Phase == PhaseListHosts && hostLine.MatchString(event.Line) {
						seen++
						if seen == 1 || seen == stages*3+2 {
							continue
						}
					}
					trimmed = append(trimmed, event)
				}
				events = trimmed
			}
			if err := validateJobHostPreview(events, stages); err != nil {
				return result, err
			}
		}

	}
	return result, nil
}

func validateJobHostPreview(events []LogEvent, stages int) error {
	pattern := regexp.MustCompile(`^\s*hosts \(([0-9]+)\):`)
	counts := []int{}
	for _, event := range events {
		if event.Phase != PhaseListHosts {
			continue
		}
		match := pattern.FindStringSubmatch(event.Line)
		if len(match) == 2 {
			count, _ := strconv.Atoi(match[1])
			counts = append(counts, count)
		}
	}
	if len(counts) != stages*3 {
		return fmt.Errorf("target-host preflight is incomplete")
	}
	for i, count := range counts {
		if count == 0 {
			return fmt.Errorf("stage %d matched no hosts during preflight", i/3+1)
		}
	}
	return nil
}
