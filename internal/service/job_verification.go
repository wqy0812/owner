package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

// DownloadVerifiedRunJob reconstructs evidence, never the executable, across a
// retry lineage. The delivered executable is the root Run's complete snapshot.
type VerifiedJobEligibility struct {
	Eligible       bool     `json:"eligible"`
	Reason         string   `json:"reason,omitempty"`
	RootRunID      string   `json:"rootRunId,omitempty"`
	StageCount     int      `json:"stageCount"`
	EvidenceRunIDs []string `json:"evidenceRunIds"`
}

func (s *ExecutionService) VerifiedRunJobEligibility(ctx context.Context, user domain.User, id string) (VerifiedJobEligibility, error) {
	result := VerifiedJobEligibility{EvidenceRunIDs: []string{}}
	err := s.verifiedRunJob(ctx, user, id, nil, &result)
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
		return result, err
	}
	if err != nil {
		result.Reason = safePreparationError(err)
	} else {
		result.Eligible = true
	}
	return result, nil
}
func (s *ExecutionService) DownloadVerifiedRunJob(ctx context.Context, user domain.User, id string, output io.Writer) error {
	return s.verifiedRunJob(ctx, user, id, output, nil)
}
func (s *ExecutionService) verifiedRunJob(ctx context.Context, user domain.User, id string, output io.Writer, summary *VerifiedJobEligibility) error {
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	allowed, err := s.store.CanViewRun(ctx, user, id)
	if err != nil {
		return err
	}
	if !allowed {
		return domain.ErrForbidden
	}
	last, err := s.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if last.Status != domain.RunSucceeded || last.ScenarioRevisionID == "" || (last.Kind != domain.RunScenario && last.Kind != domain.RunScenarioTest) {
		return fmt.Errorf("%w: 完整集群作业要求场景及业务验收成功", domain.ErrConflict)
	}
	if last.InputSnapshot["executionMode"] != "install" {
		return fmt.Errorf("%w: 完整集群搭建作业要求安装场景；升级或基线检查请下载本次作业包", domain.ErrConflict)
	}
	chain := []domain.Run{}
	seen := map[string]bool{}
	for current := last; ; {
		if seen[current.ID] {
			return fmt.Errorf("%w: retry lineage is cyclic", domain.ErrConflict)
		}
		seen[current.ID] = true
		if current.EnvironmentID != last.EnvironmentID || current.EnvironmentRevisionID != last.EnvironmentRevisionID || current.ScenarioRevisionID != last.ScenarioRevisionID || current.Kind != last.Kind || current.InputSnapshot["executionMode"] != "install" {
			return fmt.Errorf("%w: retry lineage changes the locked target", domain.ErrConflict)
		}
		chain = append(chain, current)
		if current.RetryOfRunID == "" {
			break
		}
		current, err = s.store.GetRun(ctx, current.RetryOfRunID)
		if err != nil {
			return err
		}
	}
	rootRun := chain[len(chain)-1]
	if last.RetryRootRunID != "" && last.RetryRootRunID != rootRun.ID {
		return fmt.Errorf("%w: retry root identity mismatch", domain.ErrConflict)
	}
	_, data, _, err := s.store.GetRunJob(ctx, rootRun.ID)
	if err != nil {
		return err
	}
	bundle, err := ansible.ReadJobArchive(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer bundle.Close()
	if !ansible.IsNativeJobContract(bundle.Manifest.Contract) {
		return fmt.Errorf("%w: 历史作业不是原生 Ansible 作业，需要使用当前执行器重新验证", domain.ErrConflict)
	}
	if bundle.Manifest.Plan.EnvironmentID != rootRun.EnvironmentID {
		return fmt.Errorf("%w: job environment identity mismatch", domain.ErrConflict)
	}
	rootPlan, err := mapToPlan(rootRun.InputSnapshot)
	if err != nil {
		return err
	}
	revision, err := s.store.GetScenarioRevision(ctx, last.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if locked, _ := rootRun.InputSnapshot["scenarioRevisionSpecDigest"].(string); locked == "" || locked != scenarioRevisionSpecDigest(revision) {
		return fmt.Errorf("%w: 场景内容已变化，原验证不能用于当前交付", domain.ErrConflict)
	}
	if err := s.workspaceVerifier.verifyLockedWorkspaceDigests(ctx, rootPlan.Steps); err != nil {
		return err
	}
	for _, step := range rootPlan.Steps {
		if step.SourceType == "scenario_acceptance" {
			continue
		}
		release, err := s.store.GetComponentRelease(ctx, step.ReleaseID)
		if err != nil {
			return err
		}
		if componentReleaseSpecDigest(release) != step.ReleaseSpecDigest {
			return fmt.Errorf("%w: 组件合同已变化，需要重新验证", domain.ErrConflict)
		}
	}
	canonical := map[string]ansible.JobStep{}
	acceptance := false
	for _, step := range bundle.Manifest.Plan.Steps {
		canonical[step.ID] = step
		acceptance = acceptance || step.SourceType == "scenario_acceptance"
	}
	if !acceptance {
		return fmt.Errorf("%w: 完整集群作业缺少业务验收阶段", domain.ErrConflict)
	}
	completed := map[string]bool{}
	evidence := ansible.JobVerification{ScenarioRevisionID: last.ScenarioRevisionID, RootBundleDigest: bundle.Manifest.Digest, FullPlanDigest: bundle.Manifest.FullPlanDigest}
	for index := len(chain) - 1; index >= 0; index-- {
		run := chain[index]
		locked, err := mapToPlan(run.InputSnapshot)
		if err != nil {
			return err
		}
		if locked.Runtime != bundle.Manifest.Plan.Runtime {
			return fmt.Errorf("%w: retry runtime differs from full job", domain.ErrConflict)
		}
		steps := jobPlanFromLocked(run.EnvironmentID, locked, nil).Steps
		statuses := map[string]domain.RunStatus{}
		for _, actual := range run.Steps {
			statuses[actual.NodeID] = actual.Status
		}
		for i, step := range steps {
			identity := strings.TrimPrefix(step.ID, "refresh-")
			original, ok := canonical[identity]
			if !ok || !sameVerifiedJobStep(original, step) {
				return fmt.Errorf("%w: retry changed a locked stage", domain.ErrConflict)
			}
			if status, observed := statuses[locked.Steps[i].NodeID]; observed {
				completed[identity] = status == domain.RunSucceeded
			}
		}
		evidence.RunIDs = append(evidence.RunIDs, run.ID)
	}
	for id := range canonical {
		if !completed[id] {
			return fmt.Errorf("%w: 完整作业仍有未验证阶段：%s", domain.ErrConflict, id)
		}
	}
	if last.FinishedAt == nil {
		return fmt.Errorf("%w: successful Run has no completion time", domain.ErrConflict)
	}
	evidence.CompletedAt = last.FinishedAt.UTC().Format(time.RFC3339Nano)
	if summary != nil {
		summary.RootRunID = rootRun.ID
		summary.StageCount = len(canonical)
		summary.EvidenceRunIDs = evidence.RunIDs
	}
	if output == nil {
		return nil
	}
	if err := bundle.BindVerification(evidence); err != nil {
		return err
	}
	return bundle.WriteArchive(output)
}

func sameVerifiedJobStep(original, candidate ansible.JobStep) bool {
	if candidate.ID == "refresh-"+original.ID {
		if candidate.Phase != "check" || (original.Phase != "post" && original.Phase != "check") {
			return false
		}
		candidate.ID, candidate.Phase = original.ID, original.Phase
	}
	// Role names and tasks_from are derived during compilation. All authored
	// identity, targeting, inputs, safety flags and source hashes must match.
	original.Role, original.TasksFrom = "", ""
	candidate.Role, candidate.TasksFrom = "", ""
	return digestValue(original) == digestValue(candidate)
}
