package service

import (
	"context"
	"errors"
	"fmt"

	"codex/platform-demo/internal/domain"
)

// Resolve before action expansion: backup binding happens later and must not
// make a default rollback check accidentally refer to an unrelated action.
func (r *RollbackPlanner) bindRollbackCheckSources(ctx context.Context, environmentID string, steps []lockedStep) error {
	for i := range steps {
		step := &steps[i]
		if step.Action != domain.ActionRollback || step.Phase != "" {
			continue
		}
		release, err := r.store.GetComponentRelease(ctx, step.ReleaseID)
		if err != nil {
			return err
		}
		action, ok := release.ActionByID(step.ActionID)
		if !ok {
			return domain.ErrNotFound
		}
		if action.PostCheckActionID != "" {
			continue
		}
		if step.Backup != nil {
			step.RollbackSourceActionID = step.Backup.ActionID
		}
		for j := i - 1; step.RollbackSourceActionID == "" && j >= 0; j-- {
			source := steps[j]
			if source.ComponentID != step.ComponentID || source.ReleaseID != step.ReleaseID || resourceOwner(source) != resourceOwner(*step) {
				continue
			}
			if source.Action == domain.ActionInstall || source.Action == domain.ActionUpgrade || source.Action == domain.ActionConfigure {
				step.RollbackSourceActionID = source.ActionID
				step.RollbackSourceVariables = cloneMap(source.Variables)
			}
		}
		if step.RollbackSourceActionID == "" {
			receipt, receiptErr := r.pendingReceiptForStep(ctx, environmentID, *step)
			if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
				return receiptErr
			}
			if receiptErr == nil && receipt.Status != "verified" {
				step.RollbackSourceActionID = receipt.Backup.ActionID
				backup := receipt.Backup
				step.Backup = &backup
			} else if installation, err := r.installationForStep(ctx, environmentID, *step); err == nil {
				step.RollbackSourceActionID = installation.Backup.ActionID
				backup := installation.Backup
				step.Backup = &backup
			} else if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		if step.Backup != nil && step.Backup.InstallRunID != "" {
			run, err := r.store.GetRun(ctx, step.Backup.InstallRunID)
			if err != nil {
				return err
			}
			original, err := mapToPlan(run.InputSnapshot)
			if err != nil {
				return err
			}
			matches := 0
			for _, source := range original.Steps {
				if source.Phase == "execute" && source.ComponentID == step.ComponentID && source.ReleaseID == step.ReleaseID && source.ActionID == step.RollbackSourceActionID && resourceOwner(source) == resourceOwner(*step) {
					step.RollbackSourceVariables = cloneMap(source.Variables)
					step.RollbackSourceFrozen = true
					matches++
				}
			}
			if matches != 1 {
				return fmt.Errorf("%w: 回滚来源动作与原 Run 参数无法唯一对应，请选择专用检查", domain.ErrConflict)
			}
		}
		if step.RollbackSourceActionID == "" {
			return fmt.Errorf("%w: 无法确定被回滚动作，请选择专用回滚后检查", domain.ErrConflict)
		}
	}
	return nil
}
