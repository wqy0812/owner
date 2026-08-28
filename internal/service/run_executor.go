package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func (e *RunExecutor) executeRun(run domain.Run) {
	p := e.platform
	ctx, cancel := context.WithCancel(p.rootCtx)
	p.mu.Lock()
	p.active[run.ID] = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.active, run.ID)
		p.mu.Unlock()
	}()

	plan, err := mapToPlan(run.InputSnapshot)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := p.validateLockedRollbackPlan(ctx, run, plan); err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := p.mirrorRunImages(ctx, run.ID, &plan); err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	if err := p.mirrorRunArtifacts(ctx, run.ID, &plan); err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	environmentRevision, err := p.store.GetEnvironmentRevision(ctx, run.EnvironmentRevisionID)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	inventory, err := renderInventory(environmentRevision.Inventory)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	credentialVariables, secrets, err := resolveCredentialRefs(environmentRevision.CredentialRefs)
	if err != nil {
		p.finishRun(run, domain.RunFailed, err)
		return
	}
	var sharedWorkspace *ansiblerunner.Workspace
	preparedRunner, supportsSharedWorkspace := p.runner.(workspaceRunner)
	if supportsSharedWorkspace {
		sharedWorkspace, err = preparedRunner.PrepareWorkspace(run.ArtifactDigest)
		if err != nil {
			p.finishRun(run, domain.RunFailed, err)
			return
		}
		defer sharedWorkspace.Close()
	}

	for _, locked := range plan.Steps {
		if ctx.Err() != nil {
			p.finishRun(run, domain.RunCancelled, ctx.Err())
			return
		}
		now := time.Now().UTC()
		step := domain.RunStep{ID: newID("step"), RunID: run.ID, NodeID: locked.NodeID, Name: locked.Name, Status: domain.RunRunning, StartedAt: &now}
		if err := p.store.CreateRunStep(ctx, step); err != nil {
			p.finishRun(run, domain.RunFailed, err)
			return
		}
		variables := cloneMap(locked.Variables)
		mergeMap(variables, credentialVariables)
		request := ansiblerunner.Request{
			Playbook: locked.Playbook, Inventory: inventory, Variables: variables, SecretValues: secrets,
			Limit: locked.Limit, Tags: locked.Tags, Timeout: time.Duration(locked.TimeoutSeconds) * time.Second,
			ExpectedPlaybookSHA256: locked.PlaybookDigest, ExpectedTreeSHA256: run.ArtifactDigest,
			LogSink: func(event ansiblerunner.LogEvent) {
				_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{
					RunID: run.ID, StepID: step.ID, Stream: string(event.Stream), Message: event.Line, CreatedAt: event.Time,
				})
				p.hub.Publish("run.log", map[string]any{"runId": run.ID, "stepId": step.ID, "stream": event.Stream, "line": event.Line})
			},
		}
		var result ansiblerunner.Result
		var runErr error
		if supportsSharedWorkspace {
			result, runErr = preparedRunner.RunInWorkspace(ctx, sharedWorkspace, request)
		} else {
			result, runErr = p.runner.Run(ctx, request)
		}
		exitCode := 0
		if len(result.Phases) > 0 {
			exitCode = result.Phases[len(result.Phases)-1].ExitCode
		}
		step.ExitCode = &exitCode
		finished := time.Now().UTC()
		step.FinishedAt = &finished
		if runErr != nil {
			step.Status = domain.RunFailed
			step.Summary = runErr.Error()
			if errors.Is(ctx.Err(), context.Canceled) {
				step.Status = domain.RunCancelled
			}
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
			}
			if step.Status == domain.RunCancelled {
				p.finishRun(run, domain.RunCancelled, ctx.Err())
			} else {
				p.finishRun(run, domain.RunFailed, runErr)
			}
			return
		}
		if run.ArtifactDigest != "" && result.TreeSHA256 != run.ArtifactDigest {
			step.Status, step.Summary = domain.RunFailed, "playbook tree digest changed after the run was queued"
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
			}
			p.finishRun(run, domain.RunFailed, errors.New(step.Summary))
			return
		}
		step.Status = domain.RunSucceeded
		step.Summary = recapSummary(result.Recap)
		if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
			log.Printf("run %s: update step %s: %v", run.ID, step.ID, stepErr)
		}
		if lifecycleErr := p.recordSuccessfulLifecycleStep(context.Background(), run, locked, finished); lifecycleErr != nil {
			step.Status = domain.RunFailed
			step.Summary = lifecycleErr.Error()
			if stepErr := p.store.UpdateRunStep(context.Background(), step); stepErr != nil {
				log.Printf("run %s: persist failed lifecycle step %s: %v", run.ID, step.ID, stepErr)
			}
			p.finishRun(run, domain.RunFailed, lifecycleErr)
			return
		}
		p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "stepId": step.ID, "status": step.Status})
	}
	p.finishRun(run, domain.RunSucceeded, nil)
}

func (e *RunExecutor) mirrorRunImages(ctx context.Context, runID string, plan *lockedPlan) error {
	p := e.platform
	for _, transfer := range plan.ImageTransfers {
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("mirroring image from %s to %s", transfer.SourceRegistry, transfer.TargetRegistry), CreatedAt: time.Now().UTC()})
		if err := p.imageDelivery.Probe(ctx, ImageLocation{Ref: transfer.TargetDigest}, ImageDigest{Value: transfer.TargetDigest}); err == nil {
			if resultErr := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, "reused_target", transfer.TargetDigest, "target content appeared while approval was pending"); resultErr != nil {
				return resultErr
			}
			continue
		}
		request := ImageTransfer{
			Source: ImageLocation{Ref: transfer.SourceDigest}, Target: ImageLocation{Ref: transfer.TargetRef},
			Digest: ImageDigest{Value: transfer.TargetDigest},
			Log: func(message string) {
				_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: Redact(message).(string), CreatedAt: time.Now().UTC()})
			},
		}
		if err := p.imageDelivery.Transfer(ctx, request); err != nil {
			_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", transfer.TargetDigest, err.Error())
			return err
		}
		if err := p.store.RecordComponentImageMirror(ctx, transfer.TargetRegistry, transfer.SourceDigest, transfer.TargetRef, transfer.TargetDigest, time.Now().UTC()); err != nil {
			return err
		}
		if err := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, "transferred", transfer.TargetDigest, "registry copy completed and digest verified"); err != nil {
			return err
		}
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: "image is ready at " + transfer.TargetDigest, CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (e *RunExecutor) mirrorRunArtifacts(ctx context.Context, runID string, plan *lockedPlan) error {
	p := e.platform
	for _, transfer := range plan.ArtifactTransfers {
		message := fmt.Sprintf("mirroring media %s from %s to %s", transfer.Alias, transfer.SourceURL, transfer.TargetStation)
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: message, CreatedAt: time.Now().UTC()})

		location := ArtifactLocation{FileStation: transfer.TargetStation, RelativePath: transfer.RelativePath}
		identity := ArtifactIdentity{SHA256: transfer.SHA256, SizeBytes: transfer.SizeBytes}
		err := p.artifactDelivery.Probe(ctx, location, identity)
		if err != nil && !errors.Is(err, ErrDeliveryTargetMissing) {
			_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", artifactURL(transfer.TargetStation, transfer.RelativePath), err.Error())
			return fmt.Errorf("verify target media %s: %w", transfer.Alias, err)
		}
		if errors.Is(err, ErrDeliveryTargetMissing) {
			request := ArtifactTransfer{Source: ArtifactLocation{URL: transfer.SourceURL}, Target: location, Identity: identity}
			if err := p.artifactDelivery.Transfer(ctx, request); err != nil {
				_ = e.recordDeliveryResult(context.Background(), runID, plan, transfer.RequirementID, "failed", artifactURL(transfer.TargetStation, transfer.RelativePath), err.Error())
				return fmt.Errorf("mirror media %s: %w", transfer.Alias, err)
			}
		}
		if err := p.store.RecordComponentArtifactMirror(ctx, transfer.SourceURL, transfer.TargetStation, transfer.RelativePath, transfer.SHA256, time.Now().UTC()); err != nil {
			return fmt.Errorf("record mirrored media %s: %w", transfer.Alias, err)
		}
		status, message := "transferred", "target FSS fetched and verified the content"
		if err == nil {
			status, message = "reused_target", "target content appeared while approval was pending"
		}
		if err := e.recordDeliveryResult(ctx, runID, plan, transfer.RequirementID, status, artifactURL(transfer.TargetStation, transfer.RelativePath), message); err != nil {
			return err
		}
		_, _ = p.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, Stream: "stdout", Message: fmt.Sprintf("media %s is ready on %s (sha256:%s)", transfer.Alias, transfer.TargetStation, transfer.SHA256), CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (e *RunExecutor) recordDeliveryResult(ctx context.Context, runID string, plan *lockedPlan, requirementID, status, location, message string) error {
	now := time.Now().UTC()
	for index := range plan.DeliveryResults {
		if plan.DeliveryResults[index].RequirementID == requirementID {
			plan.DeliveryResults[index].Status = status
			plan.DeliveryResults[index].ActualLocation = location
			plan.DeliveryResults[index].Message = message
			plan.DeliveryResults[index].CompletedAt = &now
			return e.platform.store.UpdateRunDeliveryResults(ctx, runID, plan.DeliveryResults)
		}
	}
	return fmt.Errorf("%w: delivery result %s is missing from locked plan", domain.ErrConflict, requirementID)
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
