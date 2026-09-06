package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func (e *RunExecutor) executeRun(run domain.Run) {
	ctx, cancel := context.WithCancel(e.rootCtx)
	e.control.register(run.ID, cancel)
	defer func() {
		cancel()
		e.control.unregister(run.ID)
	}()

	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := e.store.ValidateScenarioExecutionBaseline(ctx, run); err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := e.store.ValidateRetryState(ctx, run); err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := e.rollback.validateLockedRollbackPlan(ctx, run, plan); err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	if plan.ResourcePolicyVersion > 0 {
		if e.resourceVerifier == nil {
			e.recorder.finishRun(run, domain.RunFailed, fmt.Errorf("resource verifier is required"))
			return
		}
		if err := e.resourceVerifier.verifyRunResources(ctx, run, plan); err != nil {
			e.recorder.finishRun(run, domain.RunFailed, err)
			return
		}
	}
	// Rejoin the queued evidence with the current database contract and source
	// workspace before any delivery or target-side mutation begins.
	if err := e.workspaceVerifier.verifyLockedWorkspaceDigests(ctx, plan.Steps); err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	lifecycle := run.ScenarioRevisionID != "" && fmt.Sprint(run.InputSnapshot["scenarioContractVersion"]) == "2"
	upgradeDelivery := lifecycle && run.InputSnapshot["executionMode"] == string(domain.ScenarioExecutionUpgrade)
	pendingDelivery := len(plan.ImageTransfers) > 0 || len(plan.ArtifactTransfers) > 0
	if lifecycle && run.InputSnapshot["executionMode"] == string(domain.ScenarioExecutionBaselineVerify) && (pendingDelivery || len(plan.DeliveryRequirements) > 0) {
		e.recorder.finishRun(run, domain.RunFailed, fmt.Errorf("%w: 基线复核不能执行新的媒体交付；请先恢复目标环境媒体并重新预览", domain.ErrConflict))
		return
	}
	deliver := func() error {
		// Media copying may change the environment even if a later component
		// task never starts. Persist that fact before contacting the target.
		if lifecycle && pendingDelivery {
			if err := e.recorder.markMutation(ctx, run); err != nil {
				return err
			}
		}
		if err := e.delivery.mirrorRunImages(ctx, run.ID, &plan); err != nil {
			return err
		}
		if err := e.delivery.mirrorRunArtifacts(ctx, run.ID, &plan); err != nil {
			return err
		}
		return e.delivery.verifyLockedMedia(ctx, plan)
	}
	if !upgradeDelivery {
		if err := deliver(); err != nil {
			e.recorder.finishRun(run, domain.RunFailed, err)
			return
		}
	} else if !pendingDelivery {
		if err := e.delivery.verifyLockedMedia(ctx, plan); err != nil {
			e.recorder.finishRun(run, domain.RunFailed, err)
			return
		}
	} else if pendingDelivery {
		hasChange := false
		for _, step := range plan.Steps {
			hasChange = hasChange || step.Stage == "change"
		}
		if !hasChange {
			e.recorder.finishRun(run, domain.RunFailed, fmt.Errorf("%w: 无组件变更的升级不能执行新的媒体交付；请先恢复目标环境媒体", domain.ErrConflict))
			return
		}
	}
	environmentRevision, err := e.store.GetEnvironmentRevision(ctx, run.EnvironmentRevisionID)
	if err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	inventory, err := renderInventory(environmentRevision.Inventory)
	if err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}
	credentialVariables, secrets, err := resolveCredentialRefs(environmentRevision.CredentialRefs)
	if err != nil {
		e.recorder.finishRun(run, domain.RunFailed, err)
		return
	}

	jobPlan := jobPlanFromLocked(run.EnvironmentID, plan, inventory)
	active := map[string]domain.RunStep{}
	deliveryCompleted := !upgradeDelivery || !pendingDelivery
	var activeID atomic.Value
	var logFailure atomic.Pointer[error]
	activeID.Store("")
	request := ansiblerunner.JobRequest{Plan: jobPlan, Credentials: credentialVariables, ConnectionVariables: connectionCredentials(credentialVariables), SecretValues: secrets}
	if lifecycle && pendingDelivery {
		request.StagePreparationTimeout = 30 * time.Minute
	}
	request.LogSink = func(event ansiblerunner.LogEvent) {
		if err := e.recorder.recordOutput(run.ID, activeID.Load().(string), event); err != nil {
			logFailure.CompareAndSwap(nil, &err)
			cancel()
		}
	}
	request.OnEvent = func(event ansiblerunner.JobEvent) error {
		return e.recorder.recordEvent(ctx, run.ID, activeID.Load().(string), event)
	}
	request.OnBoundary = func(ctx context.Context, kind string, stage ansiblerunner.JobStep, result ansiblerunner.JobStepResult) error {
		locked := lockedStepByID(plan.Steps, stage.ID)
		if locked == nil {
			return fmt.Errorf("unknown locked stage")
		}
		if kind == "begin" {
			if !deliveryCompleted && locked.Stage == "change" {
				for _, source := range plan.Steps {
					if source.Stage == "source_verify" && active[source.ID].Status != domain.RunSucceeded {
						return fmt.Errorf("%w: 来源组件验证尚未全部完成，不能开始媒体交付", domain.ErrConflict)
					}
				}
				activeID.Store("")
				if err := deliver(); err != nil {
					return err
				}
				deliveryCompleted = true
			}
			step, err := e.recorder.beginStep(ctx, run, *locked, result)
			if step.ID != "" {
				active[stage.ID] = step
				activeID.Store(step.ID)
			}
			if err != nil {
				return err
			}
		} else {
			step, err := e.recorder.completeStep(ctx, run, *locked, plan.ParentSteps, active[stage.ID], result)
			if err != nil {
				return err
			}
			active[stage.ID] = step
		}

		return nil
	}
	result, runErr := e.executeLockedJob(ctx, run, request)
	if failure := logFailure.Load(); failure != nil {
		runErr = fmt.Errorf("persist execution log: %w", *failure)
	}
	e.recorder.recordFailedSteps(result.Steps, active)
	if logFailure.Load() == nil && errors.Is(ctx.Err(), context.Canceled) && (result.Canceled || errors.Is(runErr, context.Canceled)) {
		e.recorder.finishRun(run, domain.RunCancelled, ctx.Err())
		return
	}
	if runErr != nil {
		e.recorder.finishRun(run, domain.RunFailed, runErr)
		return
	}
	e.recorder.finishRun(run, domain.RunSucceeded, nil)
}

func actionRuntimeTags(tags []string) []string {
	runtimeTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "clusterforge.") {
			runtimeTags = append(runtimeTags, tag)
		}
	}
	return runtimeTags
}

func renderInventory(raw json.RawMessage) ([]byte, error) {
	var document InventoryDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: invalid environment inventory: %v", domain.ErrInvalid, err)
	}
	if len(document.Hosts) == 0 {
		return nil, fmt.Errorf("%w: inventory has no hosts", domain.ErrInvalid)
	}
	groups := map[string][]string{}
	var builder strings.Builder
	builder.WriteString("[all]\n")
	for _, host := range document.Hosts {
		if !inventoryToken(host.Name) || !inventoryToken(host.Address) {
			return nil, fmt.Errorf("%w: inventory host contains unsafe characters", domain.ErrInvalid)
		}
		builder.WriteString(host.Name)
		if host.Address == "localhost" || host.Address == "127.0.0.1" || host.Address == "::1" {
			builder.WriteString(" ansible_connection=local")
		} else {
			builder.WriteString(" ansible_host=" + host.Address)
			if host.User != "" {
				if !inventoryToken(host.User) {
					return nil, fmt.Errorf("%w: unsafe inventory user", domain.ErrInvalid)
				}
				builder.WriteString(" ansible_user=" + host.User)
			}
			if host.Port > 0 {
				builder.WriteString(" ansible_port=" + strconv.Itoa(host.Port))
			}
		}
		builder.WriteByte('\n')
		for _, group := range host.Groups {
			if !inventoryToken(group) {
				return nil, fmt.Errorf("%w: unsafe inventory group", domain.ErrInvalid)
			}
			groups[group] = append(groups[group], host.Name)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		if key != "all" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, group := range keys {
		builder.WriteString("\n[" + group + "]\n")
		for _, host := range groups[group] {
			builder.WriteString(host + "\n")
		}
	}
	return []byte(builder.String()), nil
}

func inventoryToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func resolveCredentialRefs(refs []domain.CredentialRef) (map[string]any, []string, error) {
	variables := map[string]any{}
	var secrets []string
	for _, ref := range refs {
		switch ref.Kind {
		case "envVarRef":
			value, ok := os.LookupEnv(ref.Reference)
			if !ok || value == "" {
				return nil, nil, fmt.Errorf("credential %q envVarRef is not configured", ref.Name)
			}
			variables[ref.Name] = value
			secrets = append(secrets, value)
		case "sshKeyPath":
			if _, err := os.Stat(ref.Reference); err != nil {
				return nil, nil, fmt.Errorf("credential SSH key %s: %w", ref.Name, err)
			}
			variables[ref.Name] = ref.Reference
		default:
			return nil, nil, fmt.Errorf("unsupported credential reference kind %q", ref.Kind)
		}
	}
	return variables, secrets, nil
}

func recapSummary(recap map[string]ansiblerunner.HostRecap) string {
	if len(recap) == 0 {
		return "Ansible completed successfully"
	}
	changed, okCount := 0, 0
	for _, host := range recap {
		changed += host.Changed
		okCount += host.OK
	}
	return fmt.Sprintf("recap: ok=%d changed=%d hosts=%d", okCount, changed, len(recap))
}
