package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

// Subjects contain only owned actionable contracts, current environment input,
// and directory identities. They never hydrate historical versions.
type WorkbenchSubjects struct {
	Components   []domain.Component
	Scenarios    []domain.Scenario
	Environments []domain.Environment
}

func (s *Store) WorkbenchSubjects(ctx context.Context, user domain.User) (out WorkbenchSubjects, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if user.Role == domain.RoleComponentOwner || user.Role == domain.RolePlatformAdmin {
		where, args := `owner_id=?`, []any{user.ID}
		if user.Role == domain.RolePlatformAdmin {
			where, args = `EXISTS(SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND r.status='draft' AND r.review_status='pending')`, nil
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,name,owner_id,updated_at FROM components WHERE `+where+` ORDER BY name`, args...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var c domain.Component
			var updated string
			if e = rows.Scan(&c.ID, &c.Name, &c.OwnerID, &updated); e != nil {
				rows.Close()
				return out, e
			}
			c.UpdatedAt = parseTime(updated)
			out.Components = append(out.Components, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		query, args := releaseSelect+` JOIN components c ON c.id=r.component_id WHERE r.status='draft' AND c.owner_id=? ORDER BY r.created_at DESC,r.id DESC`, []any{user.ID}
		var releases []domain.ComponentRelease
		if user.Role == domain.RolePlatformAdmin {
			// Review cards use saved submission metadata, not executable contracts.
			rows, e = tx.QueryContext(ctx, `SELECT r.id,r.component_id,r.version,l.name,r.review_contract_digest,r.review_submitted_at,r.created_at FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id WHERE r.status='draft' AND r.review_status='pending' ORDER BY r.created_at DESC,r.id DESC`)
			if e != nil {
				return out, e
			}
			for rows.Next() {
				var r domain.ComponentRelease
				var submitted sql.NullString
				var created string
				x := rows.Scan(&r.ID, &r.ComponentID, &r.Version, &r.LineName, &r.Review.ContractDigest, &submitted, &created)
				if x != nil {
					rows.Close()
					return out, x
				}
				r.Status, r.Review.Status = domain.ReleaseDraft, domain.ReleaseReviewPending
				r.CreatedAt = parseTime(created)
				if submitted.Valid {
					at := parseTime(submitted.String)
					r.Review.SubmittedAt = &at
				}
				releases = append(releases, r)
			}
			e = rows.Err()
			rows.Close()
		} else {
			releases, e = listReleases(ctx, tx, query, args...)
		}
		if e != nil {
			return out, e
		}
		byID := map[string]int{}
		for i, c := range out.Components {
			byID[c.ID] = i
		}
		for _, r := range releases {
			i := byID[r.ComponentID]
			out.Components[i].Releases = append(out.Components[i].Releases, r)
		}
	}
	if user.Role == domain.RolePlatformAdmin {
		return out, tx.Commit()
	}
	if user.Role == domain.RoleScenarioOwner {
		rows, e := tx.QueryContext(ctx, `SELECT id,name,owner_id,COALESCE(current_revision_id,''),updated_at FROM scenarios WHERE owner_id=? ORDER BY name`, user.ID)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var item domain.Scenario
			var updated string
			if e = rows.Scan(&item.ID, &item.Name, &item.OwnerID, &item.CurrentRevisionID, &updated); e != nil {
				rows.Close()
				return out, e
			}
			item.UpdatedAt = parseTime(updated)
			out.Scenarios = append(out.Scenarios, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `SELECT r.id,r.scenario_id,r.revision,r.status,r.publication_generation,r.graph_json,r.environment_constraints_json,r.created_at,r.test_passed_at,r.released_at,r.deprecated_at,r.abandoned_at,r.lifecycle_json FROM scenario_revisions r JOIN scenarios s ON s.current_revision_id=r.id WHERE s.owner_id=? AND r.status IN ('draft','testing','test_passed')`, user.ID)
		if e != nil {
			return out, e
		}
		byID := map[string]int{}
		for i, sc := range out.Scenarios {
			byID[sc.ID] = i
		}
		for rows.Next() {
			r, x := scanScenarioRevision(rows)
			if x != nil {
				rows.Close()
				return out, x
			}
			i := byID[r.ScenarioID]
			out.Scenarios[i].Revisions = append(out.Scenarios[i].Revisions, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	// Environment names and ownership are needed for run cards for every role.
	rows, e := tx.QueryContext(ctx, `SELECT id,name,owner_id,COALESCE(current_revision_id,''),updated_at FROM environments WHERE archived_at IS NULL ORDER BY name`)
	if e != nil {
		return out, e
	}
	envByID := map[string]int{}
	for rows.Next() {
		var env domain.Environment
		var updated string
		if e = rows.Scan(&env.ID, &env.Name, &env.OwnerID, &env.CurrentRevisionID, &updated); e != nil {
			rows.Close()
			return out, e
		}
		env.UpdatedAt = parseTime(updated)
		envByID[env.ID] = len(out.Environments)
		out.Environments = append(out.Environments, env)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if user.Role == domain.RoleEnvironmentOwner {
		rows, e = tx.QueryContext(ctx, `SELECT r.id,r.environment_id,r.revision,r.inventory_json,r.variables_json,r.created_by,r.change_reason,r.created_at FROM environment_revisions r JOIN environments e ON e.current_revision_id=r.id WHERE e.owner_id=? AND e.archived_at IS NULL`, user.ID)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var r domain.EnvironmentRevision
			var inventory, variables, created string
			if e = rows.Scan(&r.ID, &r.EnvironmentID, &r.Revision, &inventory, &variables, &r.CreatedBy, &r.ChangeReason, &created); e != nil {
				rows.Close()
				return out, e
			}
			r.Inventory = json.RawMessage(inventory)
			r.Variables = decodeJSON(variables, map[string]string{})
			r.CreatedAt = parseTime(created)
			out.Environments[envByID[r.EnvironmentID]].Revision = &r
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `SELECT h.environment_id,h.environment_revision_id,h.status,h.checked_at,(SELECT COUNT(*) FROM json_each(h.results_json) WHERE json_extract(value,'$.reachable')=0) FROM environment_health_checks h JOIN environments e ON e.id=h.environment_id WHERE e.owner_id=? AND e.archived_at IS NULL AND h.id=(SELECT id FROM environment_health_checks WHERE environment_id=e.id ORDER BY checked_at DESC LIMIT 1)`, user.ID)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var h domain.EnvironmentHealthCheck
			var at string
			var failed int
			if e = rows.Scan(&h.EnvironmentID, &h.EnvironmentRevisionID, &h.Status, &at, &failed); e != nil {
				rows.Close()
				return out, e
			}
			h.CheckedAt = parseTime(at)
			h.Results = make([]domain.EnvironmentEndpointCheck, failed)
			out.Environments[envByID[h.EnvironmentID]].HealthCheck = &h
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `SELECT h.environment_id,h.environment_revision_id,h.status,h.checked_at,(SELECT COUNT(*) FROM json_each(h.results_json) WHERE COALESCE(json_extract(value,'$.status'),'') NOT IN ('passed','skipped')) FROM environment_ssh_checks h JOIN environments e ON e.id=h.environment_id WHERE e.owner_id=? AND e.archived_at IS NULL AND h.id=(SELECT id FROM environment_ssh_checks WHERE environment_id=e.id ORDER BY checked_at DESC LIMIT 1)`, user.ID)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var h domain.EnvironmentSSHCheck
			var at string
			var failed int
			if e = rows.Scan(&h.EnvironmentID, &h.EnvironmentRevisionID, &h.Status, &at, &failed); e != nil {
				rows.Close()
				return out, e
			}
			h.CheckedAt = parseTime(at)
			h.Results = make([]domain.EnvironmentSSHHostCheck, failed)
			out.Environments[envByID[h.EnvironmentID]].SSHCheck = &h
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}

func readWorkbenchRuns(ctx context.Context, q queryer, query string, args ...any) ([]domain.Run, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Run{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) WorkbenchRuns(ctx context.Context, user domain.User) ([]domain.Run, error) {
	where, args := runVisibility(user)
	// Rank identities, not JSON plans. Retention tombstones must participate so
	// deleting a newer success never resurrects a superseded failure.
	query := `WITH visible AS MATERIALIZED (SELECT id,status,created_at,kind,environment_id,component_release_id,scenario_revision_id,action_kind FROM retained_run_history runs WHERE ` + where + `), ranked AS (
SELECT id,status,ROW_NUMBER() OVER(PARTITION BY kind,environment_id,COALESCE(component_release_id,''),CASE WHEN component_release_id IS NOT NULL THEN '' ELSE COALESCE(scenario_revision_id,'') END,CASE WHEN component_release_id IS NOT NULL THEN action_kind ELSE '' END ORDER BY created_at DESC,id DESC) AS position FROM visible)
`
	selection := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1)
	selection = strings.Replace(selection, "input_snapshot_json", `CASE WHEN status IN ('running','queued','awaiting_approval','failed','interrupted') THEN input_snapshot_json ELSE '{"cleaned":true}' END`, 1)
	query += selection + ` WHERE id IN (SELECT id FROM ranked WHERE position=1 OR status IN ('running','queued','awaiting_approval')) ORDER BY created_at DESC,id DESC`
	return readWorkbenchRuns(ctx, s.db, query, args...)
}

func (s *Store) WorkbenchComponentRun(ctx context.Context, user domain.User, releaseID, digest string) ([]domain.Run, error) {
	where, args := runVisibility(user)
	args = append([]any{releaseID, digest}, args...)
	query := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1) + ` WHERE component_release_id=? AND component_spec_digest=? AND kind='component_test' AND (` + where + `) ORDER BY created_at DESC,id DESC LIMIT 1`
	return readWorkbenchRuns(ctx, s.db, query, args...)
}

// No fixed history cutoff: callers advance this keyset until the required
// exact-contract evidence is found or candidates are exhausted.
func (s *Store) WorkbenchScenarioRuns(ctx context.Context, user domain.User, revisionID, digest string, success bool, before time.Time, beforeID string) ([]domain.Run, error) {
	where, args := runVisibility(user)
	where = `scenario_revision_id=? AND kind='scenario_test' AND (` + where + `)`
	args = append([]any{revisionID}, args...)
	if digest != "" {
		where += ` AND scenario_spec_digest=?`
		args = append(args, digest)
	}
	if success {
		where += ` AND status='succeeded'`
	}
	if !before.IsZero() {
		where += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, timeText(before), timeText(before), beforeID)
	}
	query := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1) + ` WHERE ` + where + ` ORDER BY created_at DESC,id DESC LIMIT 64`
	return readWorkbenchRuns(ctx, s.db, query, args...)
}

type WorkbenchMetadata struct {
	Components map[string]domain.Component
	Releases   map[string]domain.ComponentRelease
	Scenarios  map[string]domain.Scenario
	Revisions  map[string]domain.ScenarioRevision
}

func (s *Store) WorkbenchMetadata(ctx context.Context, user domain.User, runs []domain.Run) (out WorkbenchMetadata, err error) {
	out = WorkbenchMetadata{map[string]domain.Component{}, map[string]domain.ComponentRelease{}, map[string]domain.Scenario{}, map[string]domain.ScenarioRevision{}}
	releaseIDs, revisionIDs := map[string]bool{}, map[string]bool{}
	for _, r := range runs {
		if r.ComponentReleaseID != "" {
			releaseIDs[r.ComponentReleaseID] = true
		}
		if r.ScenarioRevisionID != "" {
			revisionIDs[r.ScenarioRevisionID] = true
		}
	}
	// Metadata joins are bounded by selected work items, not catalog history.
	for ids := rangeChunks(releaseIDs); len(ids) > 0; ids = ids[1:] {
		batch := ids[0]
		args := stringArgs(batch)
		rows, e := s.db.QueryContext(ctx, `SELECT r.id,r.component_id,r.version,r.status,r.candidate,r.review_status,c.id,c.name,c.owner_id FROM component_releases r JOIN components c ON c.id=r.component_id WHERE r.id IN (`+sqlPlaceholders(len(batch))+`)`, args...)
		if e != nil {
			return out, e
		}
		candidates := []string{}
		for rows.Next() {
			var r domain.ComponentRelease
			var c domain.Component
			if e = rows.Scan(&r.ID, &r.ComponentID, &r.Version, &r.Status, &r.Candidate, &r.Review.Status, &c.ID, &c.Name, &c.OwnerID); e != nil {
				rows.Close()
				return out, e
			}
			owner := user.Role == domain.RolePlatformAdmin || (user.Role == domain.RoleComponentOwner && user.ID == c.OwnerID)
			if owner || r.Status == domain.ReleaseReleased {
				out.Components[c.ID] = c
				out.Releases[r.ID] = r
			} else if r.Candidate && r.Review.Status == domain.ReleaseReviewApproved {
				out.Components[c.ID] = c
				candidates = append(candidates, r.ID)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(candidates) > 0 {
			releases, e := listReleases(ctx, s.db, releaseSelect+` WHERE r.id IN (`+sqlPlaceholders(len(candidates))+`)`, stringArgs(candidates)...)
			if e != nil {
				return out, e
			}
			for _, r := range releases {
				if r.IsApprovedCandidate() {
					out.Releases[r.ID] = r
				}
			}
		}
	}
	for ids := rangeChunks(revisionIDs); len(ids) > 0; ids = ids[1:] {
		batch := ids[0]
		rows, e := s.db.QueryContext(ctx, `SELECT r.id,r.scenario_id,r.revision,r.status,s.name,s.owner_id FROM scenario_revisions r JOIN scenarios s ON s.id=r.scenario_id WHERE r.id IN (`+sqlPlaceholders(len(batch))+`)`, stringArgs(batch)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var r domain.ScenarioRevision
			var sc domain.Scenario
			if e = rows.Scan(&r.ID, &r.ScenarioID, &r.Revision, &r.Status, &sc.Name, &sc.OwnerID); e != nil {
				rows.Close()
				return out, e
			}
			sc.ID = r.ScenarioID
			if user.Role == domain.RolePlatformAdmin || (user.Role == domain.RoleScenarioOwner && user.ID == sc.OwnerID) || r.Status == domain.RevisionReleased {
				out.Scenarios[sc.ID] = sc
				out.Revisions[r.ID] = r
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

func rangeChunks(ids map[string]bool) [][]string {
	var out [][]string
	for id := range ids {
		if len(out) == 0 || len(out[len(out)-1]) == releaseReadBatchSize {
			out = append(out, []string{})
		}
		out[len(out)-1] = append(out[len(out)-1], id)
	}
	return out
}
func stringArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
func sqlPlaceholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

type WorkbenchContracts struct {
	Releases  []domain.ComponentRelease
	Revisions []domain.ScenarioRevision
}

// Full contracts are loaded only after candidate selection. Revision members
// are included in the same batch so a scene with many nodes does not cause N+1 reads.
func (s *Store) WorkbenchContracts(ctx context.Context, releases, revisions map[string]bool) (out WorkbenchContracts, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, batch := range rangeChunks(revisions) {
		rows, e := tx.QueryContext(ctx, `SELECT id,scenario_id,revision,status,publication_generation,graph_json,environment_constraints_json,created_at,test_passed_at,released_at,deprecated_at,abandoned_at,lifecycle_json FROM scenario_revisions WHERE id IN (`+sqlPlaceholders(len(batch))+`)`, stringArgs(batch)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			r, e := scanScenarioRevision(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			out.Revisions = append(out.Revisions, r)
			for _, node := range r.Graph.Nodes {
				releases[node.ReleaseID] = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	for _, batch := range rangeChunks(releases) {
		items, e := listReleases(ctx, tx, releaseSelect+` WHERE r.id IN (`+sqlPlaceholders(len(batch))+`)`, stringArgs(batch)...)
		if e != nil {
			return out, e
		}
		out.Releases = append(out.Releases, items...)
	}
	return out, tx.Commit()
}

func (s *Store) WorkbenchFailedNodes(ctx context.Context, ids map[string]bool) (map[string]string, error) {
	out := map[string]string{}
	for id := range ids {
		out[id] = ""
	}
	for _, batch := range rangeChunks(ids) {
		rows, err := s.db.QueryContext(ctx, `SELECT run_id,node_id FROM run_steps WHERE status='failed' AND run_id IN (`+sqlPlaceholders(len(batch))+`) ORDER BY rowid`, stringArgs(batch)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, node string
			if err = rows.Scan(&id, &node); err != nil {
				rows.Close()
				return nil, err
			}
			if out[id] == "" {
				out[id] = node
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
