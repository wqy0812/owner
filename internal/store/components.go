package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateComponent(ctx context.Context, c domain.Component) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO components(id,slug,name,description,layer,category,component_kind,requiredness,owner_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.ID, c.Slug, c.Name, c.Description, c.Layer, c.Category, c.Kind, c.Requiredness, c.OwnerID, timeText(c.CreatedAt), timeText(c.UpdatedAt))
	return mapSQLError(err)
}

func (s *Store) UpdateComponent(ctx context.Context, c domain.Component) error {
	res, err := s.db.ExecContext(ctx, `UPDATE components SET slug=?,name=?,description=?,layer=?,category=?,component_kind=?,requiredness=?,updated_at=? WHERE id=?`, c.Slug, c.Name, c.Description, c.Layer, c.Category, c.Kind, c.Requiredness, timeText(c.UpdatedAt), c.ID)
	if err != nil {
		return mapSQLError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error) {
	var c domain.Component
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,slug,name,description,layer,category,component_kind,requiredness,owner_id,created_at,updated_at FROM components WHERE id=?`, id).Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Layer, &c.Category, &c.Kind, &c.Requiredness, &c.OwnerID, &created, &updated)
	if err != nil {
		return c, mapSQLError(err)
	}
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	if includeReleases {
		c.Releases, err = s.ListComponentReleases(ctx, id, false)
	}
	return c, err
}

func (s *Store) ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error) {
	query := `SELECT id,slug,name,description,layer,category,component_kind,requiredness,owner_id,created_at,updated_at FROM components`
	var args []any
	if viewer.Role == domain.RoleComponentOwner {
		query += ` WHERE owner_id=? OR EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND r.status='released')`
		args = append(args, viewer.ID)
	} else {
		query += ` WHERE EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND r.status='released')`
	}
	query += ` ORDER BY CASE layer WHEN 'host_foundation' THEN 1 WHEN 'runtime_state' THEN 2 WHEN 'orchestration_core' THEN 3 WHEN 'cluster_service' THEN 4 WHEN 'observability_management' THEN 5 WHEN 'platform_extension' THEN 6 ELSE 7 END, category, name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Component
	for rows.Next() {
		var c domain.Component
		var cr, up string
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Layer, &c.Category, &c.Kind, &c.Requiredness, &c.OwnerID, &cr, &up); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(cr)
		c.UpdatedAt = parseTime(up)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		releasedOnly := viewer.Role != domain.RoleComponentOwner || out[i].OwnerID != viewer.ID
		out[i].Releases, err = s.ListComponentReleases(ctx, out[i].ID, releasedOnly)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) CreateComponentRelease(ctx context.Context, r domain.ComponentRelease) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO component_releases(id,component_id,version,release_type,status,release_notes,breaking,verified,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.ComponentID, r.Version, r.Type, r.Status, r.ReleaseNotes, r.Breaking, r.Verified, r.RiskLevel, jsonText(r.EnvironmentConstraints), jsonText(r.Parameters), timeText(r.CreatedAt), ptrTimeText(r.ReleasedAt), ptrTimeText(r.DeprecatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err = replaceReleaseChildren(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateDraftRelease(ctx context.Context, r domain.ComponentRelease) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE component_releases SET version=?,release_type=?,release_notes=?,breaking=?,verified=?,risk_level=?,environment_constraints_json=?,parameters_json=? WHERE id=? AND status='draft'`, r.Version, r.Type, r.ReleaseNotes, r.Breaking, r.Verified, r.RiskLevel, jsonText(r.EnvironmentConstraints), jsonText(r.Parameters), r.ID)
	if err != nil {
		return mapSQLError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: release is not a mutable draft", domain.ErrConflict)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM component_dependencies WHERE release_id=?`, r.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM action_definitions WHERE release_id=?`, r.ID); err != nil {
		return err
	}
	if err = replaceReleaseChildren(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceReleaseChildren(ctx context.Context, tx *sql.Tx, r domain.ComponentRelease) error {
	for _, d := range r.Dependencies {
		mappings := d.ParameterMappings
		if mappings == nil {
			mappings = []domain.ParameterMapping{}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO component_dependencies(id,release_id,upstream_component_id,upstream_release_id,purpose,parameter_mappings_json) VALUES(?,?,?,?,?,?)`, d.ID, r.ID, d.UpstreamComponentID, d.UpstreamReleaseID, d.Purpose, jsonText(mappings))
		if err != nil {
			return mapSQLError(err)
		}
	}
	for _, a := range r.Actions {
		_, err := tx.ExecContext(ctx, `INSERT INTO action_definitions(id,release_id,name,kind,playbook,tags_json,limit_pattern,host_group,allowed_parameters_json,required_credentials_json,timeout_seconds,risk_level,destructive,from_release_id,to_release_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ID, r.ID, a.Name, a.Kind, a.Playbook, jsonText(a.Tags), a.Limit, a.HostGroup, jsonText(a.AllowedParameters), jsonText(a.RequiredCredentials), a.TimeoutSeconds, a.RiskLevel, a.Destructive, nullString(a.FromReleaseID), nullString(a.ToReleaseID))
		if err != nil {
			return mapSQLError(err)
		}
	}
	return nil
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,component_id,version,release_type,status,release_notes,breaking,verified,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at FROM component_releases WHERE id=?`, id)
	r, err := scanRelease(row)
	if err != nil {
		return r, mapSQLError(err)
	}
	r.Dependencies, err = s.listDependencies(ctx, id)
	if err != nil {
		return r, err
	}
	r.Actions, err = s.listActions(ctx, id)
	return r, err
}

type ReleaseDisplayMetadata struct {
	ComponentID   string
	ComponentName string
	Version       string
}

// ListReleaseDisplayMetadata provides the release/component fields needed by
// scenario graph DTOs in one query, without loading release child records.
func (s *Store) ListReleaseDisplayMetadata(ctx context.Context) (map[string]ReleaseDisplayMetadata, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT r.id,r.component_id,c.name,r.version
FROM component_releases r JOIN components c ON c.id=r.component_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make(map[string]ReleaseDisplayMetadata)
	for rows.Next() {
		var id string
		var metadata ReleaseDisplayMetadata
		if err := rows.Scan(&id, &metadata.ComponentID, &metadata.ComponentName, &metadata.Version); err != nil {
			return nil, err
		}
		output[id] = metadata
	}
	return output, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanRelease(row scanner) (domain.ComponentRelease, error) {
	var r domain.ComponentRelease
	var breaking, verified int
	var constraints, parameters, created string
	var released, deprecated sql.NullString
	err := row.Scan(&r.ID, &r.ComponentID, &r.Version, &r.Type, &r.Status, &r.ReleaseNotes, &breaking, &verified, &r.RiskLevel, &constraints, &parameters, &created, &released, &deprecated)
	r.Breaking = breaking != 0
	r.Verified = verified != 0
	r.EnvironmentConstraints = decodeJSON(constraints, map[string]any{})
	r.Parameters = decodeJSON(parameters, []domain.ParameterDefinition{})
	r.CreatedAt = parseTime(created)
	r.ReleasedAt = parseNullTime(released)
	r.DeprecatedAt = parseNullTime(deprecated)
	return r, err
}

func (s *Store) ListComponentReleases(ctx context.Context, componentID string, releasedOnly bool) ([]domain.ComponentRelease, error) {
	q := `SELECT id,component_id,version,release_type,status,release_notes,breaking,verified,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at FROM component_releases WHERE component_id=?`
	if releasedOnly {
		q += ` AND status='released'`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, componentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ComponentRelease
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Dependencies, err = s.listDependencies(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Actions, err = s.listActions(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) listDependencies(ctx context.Context, releaseID string) ([]domain.ComponentDependency, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.release_id,d.upstream_component_id,d.upstream_release_id,d.purpose,d.parameter_mappings_json,c.name,ur.version FROM component_dependencies d JOIN components c ON c.id=d.upstream_component_id JOIN component_releases ur ON ur.id=d.upstream_release_id WHERE d.release_id=? ORDER BY c.name`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ComponentDependency
	for rows.Next() {
		var d domain.ComponentDependency
		var mappings string
		if err := rows.Scan(&d.ID, &d.ReleaseID, &d.UpstreamComponentID, &d.UpstreamReleaseID, &d.Purpose, &mappings, &d.UpstreamComponentName, &d.UpstreamVersion); err != nil {
			return nil, err
		}
		d.ParameterMappings = decodeJSON(mappings, []domain.ParameterMapping{})
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) listActions(ctx context.Context, releaseID string) ([]domain.ActionDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,release_id,name,kind,playbook,tags_json,limit_pattern,host_group,allowed_parameters_json,required_credentials_json,timeout_seconds,risk_level,destructive,from_release_id,to_release_id FROM action_definitions WHERE release_id=? ORDER BY kind,name`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ActionDefinition
	for rows.Next() {
		var a domain.ActionDefinition
		var tags, allowed, requiredCredentials string
		var destructive int
		var from, to sql.NullString
		if err := rows.Scan(&a.ID, &a.ReleaseID, &a.Name, &a.Kind, &a.Playbook, &tags, &a.Limit, &a.HostGroup, &allowed, &requiredCredentials, &a.TimeoutSeconds, &a.RiskLevel, &destructive, &from, &to); err != nil {
			return nil, err
		}
		a.Tags = decodeJSON(tags, []string{})
		a.AllowedParameters = decodeJSON(allowed, []string{})
		a.RequiredCredentials = decodeJSON(requiredCredentials, []string{})
		a.Destructive = destructive != 0
		a.FromReleaseID = from.String
		a.ToReleaseID = to.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) PublishComponentRelease(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE component_releases SET status='released',released_at=? WHERE id=? AND status='draft'`, timeText(at), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: only draft releases can be published", domain.ErrConflict)
	}
	return nil
}

func (s *Store) DeprecateComponentRelease(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE component_releases SET status='deprecated',deprecated_at=? WHERE id=? AND status='released'`, timeText(at), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: only released versions can be deprecated", domain.ErrConflict)
	}
	return nil
}
func (s *Store) MarkReleaseVerified(ctx context.Context, id string, verified bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE component_releases SET verified=? WHERE id=?`, verified, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type DependencyLink struct{ DownstreamComponentID, DownstreamComponentName, DownstreamOwnerID, UpstreamComponentID string }

func (s *Store) ListReleasedDependencyLinks(ctx context.Context) ([]DependencyLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.component_id,c.name,c.owner_id,d.upstream_component_id FROM component_dependencies d JOIN component_releases r ON r.id=d.release_id JOIN components c ON c.id=r.component_id WHERE r.status='released'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DependencyLink
	for rows.Next() {
		var l DependencyLink
		if err := rows.Scan(&l.DownstreamComponentID, &l.DownstreamComponentName, &l.DownstreamOwnerID, &l.UpstreamComponentID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) LatestReleasedVersion(ctx context.Context, componentID string) (string, error) {
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT version FROM component_releases WHERE component_id=? AND status='released' ORDER BY released_at DESC LIMIT 1`, componentID).Scan(&version)
	return version, mapSQLError(err)
}
