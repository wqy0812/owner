package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"fmt"
)

func validateScenarioAdaptationTx(ctx context.Context, tx *sql.Tx, revision domain.ScenarioRevision, previous map[string]any, complete bool) error {
	if err := validateBranchScopeTx(ctx, tx, "scenarios", revision.ScenarioID, revision.EnvironmentConstraints); err != nil {
		return err
	}
	catalog, err := loadCatalogDefinitions(ctx, tx)
	if err != nil {
		return err
	}
	if err = catalog.Options.ValidateConstraints(revision.EnvironmentConstraints); err != nil {
		return err
	}
	if err = catalog.Options.ValidateConstraintChanges(revision.EnvironmentConstraints, previous); err != nil {
		return err
	}
	if !complete {
		return nil
	}
	intersections := map[string]map[string]bool{}
	for _, node := range revision.Graph.Nodes {
		var raw, name string
		if err = tx.QueryRowContext(ctx, `SELECT r.environment_constraints_json,c.name FROM component_releases r JOIN components c ON c.id=r.component_id WHERE r.id=?`, node.ReleaseID).Scan(&raw, &name); err != nil {
			return mapSQLError(err)
		}
		constraints := decodeJSON(raw, map[string]any{})
		if issues := domain.ScenarioAdaptationIssues(revision.EnvironmentConstraints, constraints, node.ID, name, complete); len(issues) > 0 {
			return &domain.ValidationError{Message: "场景适配标签不匹配", Details: issues}
		}
		for key, rawValues := range constraints {
			values := domain.ConstraintValues(rawValues)
			if len(values) == 0 {
				continue
			}
			next := map[string]bool{}
			for _, v := range values {
				if old, exists := intersections[key]; !exists || old[v] {
					next[v] = true
				}
			}
			if len(next) == 0 {
				return fmt.Errorf("%w: 场景组件在适配标签 %s 上没有共同支持范围", domain.ErrInvalid, key)
			}
			intersections[key] = next
		}
	}
	return nil
}

// Recheck at the actual write boundary, including JSON-locked identities.
func validateRunAdaptationTx(ctx context.Context, tx *sql.Tx, run domain.Run) error {
	if run.Status != domain.RunQueued && run.Status != domain.RunAwaitingApproval && run.Status != domain.RunRunning {
		return nil
	}
	env, err := environmentRevisionTx(ctx, tx, run.EnvironmentRevisionID)
	if err != nil {
		return err
	}
	catalog, err := loadCatalogDefinitions(ctx, tx)
	if err != nil {
		return err
	}
	if err = catalog.Options.ValidateFacts(env.Facts, true); err != nil {
		return err
	}
	if run.ScenarioRevisionID != "" {
		revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
		if err != nil {
			return err
		}
		if err = validateScenarioAdaptationTx(ctx, tx, revision, revision.EnvironmentConstraints, true); err != nil {
			return err
		}
		if err = domain.MatchEnvironment(revision.EnvironmentConstraints, env.Facts); err != nil {
			return err
		}
		if digest, _ := run.InputSnapshot["scenarioRevisionSpecDigest"].(string); digest != "" && digest != domain.ScenarioRevisionSpecDigest(revision) {
			return fmt.Errorf("%w: 场景适配标签或图已变化，请重新预览", domain.ErrConflict)
		}
	}
	ids := map[string]bool{}
	if run.ComponentReleaseID != "" {
		ids[run.ComponentReleaseID] = true
	}
	if steps, ok := run.InputSnapshot["steps"].([]any); ok {
		for _, v := range steps {
			if row, ok := v.(map[string]any); ok {
				if row["sourceType"] == "scenario_acceptance" {
					continue
				}
				if id, ok := row["releaseId"].(string); ok && id != "" {
					if digest, ok := row["releaseSpecDigest"].(string); ok && digest != "" {
						release, e := getComponentRelease(ctx, tx, id)
						if e != nil {
							return e
						}
						if domain.ComponentReleaseSpecDigest(release) != digest {
							return fmt.Errorf("%w: 锁定组件合同已变化", domain.ErrConflict)
						}
					}
					ids[id] = true
				}
			}
		}
	}
	for id := range ids {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT environment_constraints_json FROM component_releases WHERE id=?`, id).Scan(&raw); err != nil {
			return mapSQLError(err)
		}
		if err = domain.MatchEnvironment(decodeJSON(raw, map[string]any{}), env.Facts); err != nil {
			return err
		}
	}
	return nil
}

func validateScenarioAdaptationRead(ctx context.Context, q queryer, revision domain.ScenarioRevision) error {
	catalog, err := loadCatalogDefinitions(ctx, q)
	if err != nil {
		return err
	}
	if err = catalog.Options.ValidateConstraints(revision.EnvironmentConstraints); err != nil {
		return err
	}
	for _, n := range revision.Graph.Nodes {
		var raw string
		if err = q.QueryRowContext(ctx, `SELECT environment_constraints_json FROM component_releases WHERE id=?`, n.ReleaseID).Scan(&raw); err != nil {
			return err
		}
		if issues := domain.ScenarioAdaptationIssues(revision.EnvironmentConstraints, decodeJSON(raw, map[string]any{}), n.ID, n.Name, true); len(issues) > 0 {
			return &domain.ValidationError{Message: "场景适配标签不匹配", Details: issues}
		}
	}
	return nil
}
