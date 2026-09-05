package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateScenario(ctx context.Context, sc domain.Scenario, rev domain.ScenarioRevision) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO scenarios(id,slug,name,description,owner_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, sc.ID, sc.Slug, sc.Name, sc.Description, sc.OwnerID, rev.ID, timeText(sc.CreatedAt), timeText(sc.UpdatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err = insertScenarioRevision(ctx, tx, rev); err != nil {
		return err
	}
	return tx.Commit()
}

type ScenarioDeletionImpact struct {
	RevisionCount          int
	PublishedRevisionCount int
	RunCount               int
}

func (s *Store) ScenarioDeletionImpact(ctx context.Context, scenarioID string) (ScenarioDeletionImpact, error) {
	var impact ScenarioDeletionImpact
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenarios WHERE id=?`, scenarioID).Scan(&exists); err != nil {
		return impact, err
	}
	if exists == 0 {
		return impact, domain.ErrNotFound
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(CASE WHEN status IN ('released','deprecated') OR released_at IS NOT NULL THEN 1 ELSE 0 END),0)
FROM scenario_revisions WHERE scenario_id=?`, scenarioID).Scan(&impact.RevisionCount, &impact.PublishedRevisionCount); err != nil {
		return impact, err
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM retained_run_history r
JOIN scenario_revisions sr ON sr.id=r.scenario_revision_id
WHERE sr.scenario_id=?`, scenarioID).Scan(&impact.RunCount); err != nil {
		return impact, err
	}
	return impact, nil
}

// DeleteScenario removes only scenarios that have never been published or run.
// The audit event is committed in the same transaction so this destructive
// action cannot succeed without leaving a retained record.
func (s *Store) DeleteScenario(ctx context.Context, scenarioID string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists, published, runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenarios WHERE id=?`, scenarioID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return domain.ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM scenario_revisions
WHERE scenario_id=? AND (status IN ('released','deprecated') OR released_at IS NOT NULL)`, scenarioID).Scan(&published); err != nil {
		return err
	}
	if published > 0 {
		return fmt.Errorf("%w: published scenario revisions must be retained", domain.ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM retained_run_history r
JOIN scenario_revisions sr ON sr.id=r.scenario_revision_id
WHERE sr.scenario_id=?`, scenarioID).Scan(&runs); err != nil {
		return err
	}
	if runs > 0 {
		return fmt.Errorf("%w: scenario run history must be retained", domain.ErrConflict)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM scenarios WHERE id=?`, scenarioID)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	return tx.Commit()
}

func insertScenarioRevision(ctx context.Context, tx *sql.Tx, r domain.ScenarioRevision) error {
	if err := validateScenarioAdaptationTx(ctx, tx, r, nil, false); err != nil {
		return err
	}
	if err := validateScenarioCatalogTx(ctx, tx, r.Graph, domain.ScenarioGraph{}); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO scenario_revisions(id,scenario_id,revision,status,graph_json,environment_constraints_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.ScenarioID, r.Revision, r.Status, jsonText(r.Graph), jsonText(r.EnvironmentConstraints), timeText(r.CreatedAt), ptrTimeText(r.TestPassedAt), ptrTimeText(r.ReleasedAt), ptrTimeText(r.DeprecatedAt), ptrTimeText(r.AbandonedAt))
	return mapSQLError(err)
}

func (s *Store) CreateScenarioRevision(ctx context.Context, r domain.ScenarioRevision) error {
	return s.CreateScenarioRevisionFromSource(ctx, "", r)
}

// CreateScenarioRevisionFromSource creates the next Draft atomically. A
// Test Passed source still occupies the scenario's single active-revision
// slot, so cloning it first retains the immutable record as an abandoned
// historical revision while preserving test_passed_at and its Run links.
func (s *Store) CreateScenarioRevisionFromSource(ctx context.Context, sourceRevisionID string, r domain.ScenarioRevision) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if sourceRevisionID != "" {
		res, err := tx.ExecContext(ctx, `
UPDATE scenario_revisions
SET status='deprecated',deprecated_at=?,abandoned_at=?
WHERE id=? AND scenario_id=? AND status='test_passed'`, timeText(r.CreatedAt), timeText(r.CreatedAt), sourceRevisionID, r.ScenarioID)
		if err != nil {
			return err
		}
		if changed, _ := res.RowsAffected(); changed == 0 {
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM scenario_revisions WHERE id=? AND scenario_id=?`, sourceRevisionID, r.ScenarioID).Scan(&status); err != nil {
				return mapSQLError(err)
			}
			if status != string(domain.RevisionReleased) && status != string(domain.RevisionDeprecated) {
				return fmt.Errorf("%w: source scenario revision is not immutable", domain.ErrConflict)
			}
		}
	}
	if err = insertScenarioRevision(ctx, tx, r); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE scenarios SET current_revision_id=?,updated_at=? WHERE id=?`, r.ID, timeText(r.CreatedAt), r.ScenarioID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) UpdateScenario(ctx context.Context, sc domain.Scenario) error {
	res, err := s.db.ExecContext(ctx, `UPDATE scenarios SET slug=?,name=?,description=?,updated_at=? WHERE id=?`, sc.Slug, sc.Name, sc.Description, timeText(sc.UpdatedAt), sc.ID)
	if err != nil {
		return mapSQLError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error) {
	var sc domain.Scenario
	var current sql.NullString
	var cr, up string
	err := s.db.QueryRowContext(ctx, `SELECT id,slug,name,description,owner_id,current_revision_id,created_at,updated_at FROM scenarios WHERE id=?`, id).Scan(&sc.ID, &sc.Slug, &sc.Name, &sc.Description, &sc.OwnerID, &current, &cr, &up)
	if err != nil {
		return sc, mapSQLError(err)
	}
	sc.CurrentRevisionID = current.String
	sc.CreatedAt = parseTime(cr)
	sc.UpdatedAt = parseTime(up)
	if includeRevisions {
		sc.Revisions, err = s.ListScenarioRevisions(ctx, id, false)
	}
	return sc, err
}

func (s *Store) ListScenarios(ctx context.Context, viewer domain.User) ([]domain.Scenario, error) {
	q := `SELECT id,slug,name,description,owner_id,current_revision_id,created_at,updated_at FROM scenarios`
	args := []any{}
	if viewer.Role == domain.RoleScenarioOwner {
		q += ` WHERE owner_id=? OR EXISTS (SELECT 1 FROM scenario_revisions r WHERE r.scenario_id=scenarios.id AND r.status='released')`
		args = append(args, viewer.ID)
	} else if viewer.Role != domain.RolePlatformAdmin {
		q += ` WHERE EXISTS (SELECT 1 FROM scenario_revisions r WHERE r.scenario_id=scenarios.id AND r.status='released')`
	}
	q += ` ORDER BY name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Scenario
	for rows.Next() {
		var sc domain.Scenario
		var current sql.NullString
		var cr, up string
		if err := rows.Scan(&sc.ID, &sc.Slug, &sc.Name, &sc.Description, &sc.OwnerID, &current, &cr, &up); err != nil {
			return nil, err
		}
		sc.CurrentRevisionID = current.String
		sc.CreatedAt = parseTime(cr)
		sc.UpdatedAt = parseTime(up)
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		releasedOnly := viewer.Role != domain.RolePlatformAdmin && (viewer.Role != domain.RoleScenarioOwner || out[i].OwnerID != viewer.ID)
		out[i].Revisions, err = s.ListScenarioRevisions(ctx, out[i].ID, releasedOnly)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListScenariosForImpact is an internal routing query. Unlike user-facing
// scenario listing it includes mutable revisions, because owners of draft or
// testing scenarios still need notification when a locked upstream component
// publishes a new version. Callers must not expose these revisions through
// ordinary read APIs.
func (s *Store) ListScenariosForImpact(ctx context.Context) ([]domain.Scenario, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,slug,name,description,owner_id,current_revision_id,created_at,updated_at FROM scenarios ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Scenario
	for rows.Next() {
		var scenario domain.Scenario
		var current sql.NullString
		var created, updated string
		if err := rows.Scan(&scenario.ID, &scenario.Slug, &scenario.Name, &scenario.Description, &scenario.OwnerID, &current, &created, &updated); err != nil {
			return nil, err
		}
		scenario.CurrentRevisionID = current.String
		scenario.CreatedAt = parseTime(created)
		scenario.UpdatedAt = parseTime(updated)
		out = append(out, scenario)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Revisions, err = s.ListScenarioRevisions(ctx, out[i].ID, false)
		if err != nil {
			return nil, err
		}
		active := out[i].Revisions[:0]
		for _, revision := range out[i].Revisions {
			if revision.Status != domain.RevisionDeprecated {
				active = append(active, revision)
			}
		}
		out[i].Revisions = active
	}
	return out, nil
}

func scanScenarioRevision(row scanner) (domain.ScenarioRevision, error) {
	var r domain.ScenarioRevision
	var graph, constraints, created string
	var tested, released, deprecated, abandoned sql.NullString
	err := row.Scan(&r.ID, &r.ScenarioID, &r.Revision, &r.Status, &r.PublicationGeneration, &graph, &constraints, &created, &tested, &released, &deprecated, &abandoned)
	r.EnvironmentConstraints = decodeJSON(constraints, map[string]any{})
	r.Graph = decodeJSON(graph, domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}})
	r.CreatedAt = parseTime(created)
	r.TestPassedAt = parseNullTime(tested)
	r.ReleasedAt = parseNullTime(released)
	r.DeprecatedAt = parseNullTime(deprecated)
	r.AbandonedAt = parseNullTime(abandoned)
	return r, err
}

func (s *Store) GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error) {
	return getScenarioRevision(ctx, s.db, id)
}

func getScenarioRevision(ctx context.Context, q queryer, id string) (domain.ScenarioRevision, error) {
	r, err := scanScenarioRevision(q.QueryRowContext(ctx, `SELECT id,scenario_id,revision,status,publication_generation,graph_json,environment_constraints_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at FROM scenario_revisions WHERE id=?`, id))
	return r, mapSQLError(err)
}

func (s *Store) ListScenarioRevisions(ctx context.Context, scenarioID string, releasedOnly bool) ([]domain.ScenarioRevision, error) {
	q := `SELECT id,scenario_id,revision,status,publication_generation,graph_json,environment_constraints_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at FROM scenario_revisions WHERE scenario_id=?`
	if releasedOnly {
		q += ` AND status='released'`
	}
	q += ` ORDER BY revision DESC`
	rows, err := s.db.QueryContext(ctx, q, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ScenarioRevision
	for rows.Next() {
		r, err := scanScenarioRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveScenarioGraph(ctx context.Context, id string, g domain.ScenarioGraph, constraints ...map[string]any) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT graph_json FROM scenario_revisions WHERE id=?", id).Scan(&raw); err != nil {
		return mapSQLError(err)
	}
	previous := decodeJSON(raw, domain.ScenarioGraph{})
	prior, err := getScenarioRevision(ctx, tx, id)
	if err != nil {
		return err
	}
	next := prior
	next.Graph = g
	if len(constraints) > 0 {
		next.EnvironmentConstraints = constraints[0]
	}
	if err := validateScenarioAdaptationTx(ctx, tx, next, prior.EnvironmentConstraints, false); err != nil {
		return err
	}
	if err := validateScenarioCatalogTx(ctx, tx, g, previous); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE scenario_revisions SET graph_json=?,environment_constraints_json=?,status='draft',test_passed_at=NULL,publication_generation=publication_generation+1 WHERE id=? AND status IN ('draft','testing','test_passed')`, jsonText(g), jsonText(next.EnvironmentConstraints), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: released revisions are immutable", domain.ErrConflict)
	}
	return tx.Commit()
}

func (s *Store) SetScenarioRevisionStatus(ctx context.Context, id string, from []domain.RevisionStatus, to domain.RevisionStatus, at time.Time) error {
	if len(from) == 0 {
		return domain.ErrInvalid
	}
	placeholders := ""
	testPassed, released := any(nil), any(nil)
	if to == domain.RevisionTestPassed {
		testPassed = timeText(at)
	}
	if to == domain.RevisionReleased {
		released = timeText(at)
	}
	args := []any{to, testPassed, released, id}
	for i, v := range from {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, v)
	}
	q := fmt.Sprintf(`UPDATE scenario_revisions SET status=?,test_passed_at=COALESCE(?,test_passed_at),released_at=COALESCE(?,released_at) WHERE id=? AND status IN (%s)`, placeholders)
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: invalid scenario revision transition", domain.ErrConflict)
	}
	return nil
}

func (s *Store) DeprecateScenarioRevision(ctx context.Context, id string, at time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='deprecated',deprecated_at=?,publication_generation=publication_generation+1 WHERE id=? AND status='released'`, timeText(at), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: only released scenario revisions can be deprecated", domain.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AbandonScenarioRevision(ctx context.Context, id string, at time.Time) (string, error) {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var scenarioID, status, currentID string
	if err := tx.QueryRowContext(ctx, `
SELECT r.scenario_id,r.status,s.current_revision_id
FROM scenario_revisions r JOIN scenarios s ON s.id=r.scenario_id
WHERE r.id=?`, id).Scan(&scenarioID, &status, &currentID); err != nil {
		return "", mapSQLError(err)
	}
	if currentID != id || domain.RevisionStatus(status) != domain.RevisionDraft {
		return "", fmt.Errorf("%w: only the current draft revision can be abandoned", domain.ErrConflict)
	}

	var restoredID string
	if err := tx.QueryRowContext(ctx, `
SELECT id FROM scenario_revisions
WHERE scenario_id=? AND id<>? AND status IN ('released','deprecated') AND abandoned_at IS NULL
ORDER BY revision DESC LIMIT 1`, scenarioID, id).Scan(&restoredID); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("%w: scenario has no immutable revision to restore", domain.ErrConflict)
		}
		return "", err
	}

	res, err := tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='deprecated',deprecated_at=?,abandoned_at=? WHERE id=? AND status='draft'`, timeText(at), timeText(at), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("%w: only a draft revision can be abandoned", domain.ErrConflict)
	}
	res, err = tx.ExecContext(ctx, `UPDATE scenarios SET current_revision_id=?,updated_at=? WHERE id=? AND current_revision_id=?`, restoredID, timeText(at), scenarioID, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("%w: scenario current revision changed", domain.ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return restoredID, nil
}

type ScenarioReference struct{ ScenarioID, ScenarioName, OwnerID, RevisionID, ComponentReleaseID string }

func (s *Store) ListReleasedScenarioReferences(ctx context.Context) ([]ScenarioReference, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.name,s.owner_id,r.id,r.graph_json FROM scenarios s JOIN scenario_revisions r ON r.scenario_id=s.id WHERE r.status='released'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScenarioReference
	for rows.Next() {
		var base ScenarioReference
		var graphRaw string
		if err := rows.Scan(&base.ScenarioID, &base.ScenarioName, &base.OwnerID, &base.RevisionID, &graphRaw); err != nil {
			return nil, err
		}
		g := decodeJSON(graphRaw, domain.ScenarioGraph{})
		for _, n := range g.Nodes {
			x := base
			x.ComponentReleaseID = n.ReleaseID
			out = append(out, x)
		}
	}
	return out, rows.Err()
}
