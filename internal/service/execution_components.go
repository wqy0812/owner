package service

import (
	"context"
	"time"

	"codex/platform-demo/internal/domain"
)

type PlanBuilder struct{ platform *Platform }
type RunCreator struct{ platform *Platform }
type RunScheduler struct{ platform *Platform }
type RunExecutor struct{ platform *Platform }
type LifecycleRecorder struct{ platform *Platform }
type RollbackPlanner struct{ platform *Platform }
type ApprovalService struct{ platform *Platform }

func (p *Platform) lockAction(component domain.Component, nodeID string, release domain.ComponentRelease, action domain.ActionDefinition, variables map[string]any) (lockedStep, error) {
	return p.planBuilder.lockAction(component, nodeID, release, action, variables)
}
func (p *Platform) prepareLockedPlan(ctx context.Context, environment domain.Environment, kind domain.RunKind, runID string, capturedAt time.Time, steps []lockedStep) (lockedPlan, string, bool, error) {
	return p.planBuilder.prepareLockedPlan(ctx, environment, kind, runID, capturedAt, steps)
}
func (p *Platform) componentTestPlanDTO(ctx context.Context, environment domain.Environment, plan lockedPlan, digest string, destructive bool) ComponentTestPlan {
	return p.planBuilder.componentTestPlanDTO(ctx, environment, plan, digest, destructive)
}
func (p *Platform) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []lockedStep, resolved map[string]map[string]resolvedParameter, expectedDigest string) (domain.Run, error) {
	return p.runCreator.createRun(ctx, user, environment, kind, releaseID, revisionID, action, steps, resolved, expectedDigest)
}
func (p *Platform) bindBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	return p.rollbackPlanner.bindBackupPlan(ctx, environmentID, runID, kind, capturedAt, plan)
}
func (p *Platform) rebindRetryBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	return p.rollbackPlanner.rebindRetryBackupPlan(ctx, environmentID, runID, kind, capturedAt, plan)
}
func (p *Platform) bindInstallBackupStep(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, step *lockedStep) error {
	return p.rollbackPlanner.bindInstallBackupStep(ctx, environmentID, runID, kind, capturedAt, step)
}
func (p *Platform) validateCurrentInstallEvidence(ctx context.Context, installation domain.EnvironmentComponentInstallation) error {
	return p.rollbackPlanner.validateCurrentInstallEvidence(ctx, installation)
}
func (p *Platform) validateLockedRollbackPlan(ctx context.Context, run domain.Run, plan lockedPlan) error {
	return p.rollbackPlanner.validateLockedRollbackPlan(ctx, run, plan)
}
func (p *Platform) schedule(environmentID string) { p.runScheduler.schedule(environmentID) }
func (p *Platform) queueWatchdog()                { p.runScheduler.queueWatchdog() }
func (p *Platform) recoverEnvironmentWorker(environmentID string, staleBefore time.Time) {
	p.runScheduler.recoverEnvironmentWorker(environmentID, staleBefore)
}
func (p *Platform) touchEnvironmentWorker(environmentID string, token uint64) {
	p.runScheduler.touchEnvironmentWorker(environmentID, token)
}
func (p *Platform) environmentWorker(environmentID string, token uint64) {
	p.runScheduler.environmentWorker(environmentID, token)
}
func (p *Platform) executeRun(run domain.Run) { p.runExecutor.executeRun(run) }
func (p *Platform) mirrorRunImages(ctx context.Context, runID string, plan *lockedPlan) error {
	return p.runExecutor.mirrorRunImages(ctx, runID, plan)
}
func (p *Platform) mirrorRunArtifacts(ctx context.Context, runID string, plan *lockedPlan) error {
	return p.runExecutor.mirrorRunArtifacts(ctx, runID, plan)
}
func (p *Platform) recordSuccessfulLifecycleStep(ctx context.Context, run domain.Run, step lockedStep, installedAt time.Time) error {
	return p.lifecycleRecorder.recordSuccessfulLifecycleStep(ctx, run, step, installedAt)
}
func (p *Platform) finishRun(run domain.Run, status domain.RunStatus, cause error) {
	p.lifecycleRecorder.finishRun(run, status, cause)
}
func (p *Platform) CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error) {
	return p.approvals.CancelRun(ctx, user, runID)
}
func (p *Platform) DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error) {
	return p.approvals.DecideApproval(ctx, user, approvalID, decision, reason, deliveryDecisions)
}
func (p *Platform) BatchDecideApprovals(ctx context.Context, user domain.User, approvalIDs []string, decision, reason string) ([]domain.Run, error) {
	return p.approvals.BatchDecideApprovals(ctx, user, approvalIDs, decision, reason)
}
