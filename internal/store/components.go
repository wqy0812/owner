package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateComponent(ctx context.Context, c domain.Component) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO components(id,slug,name,description,layer,tags_json,owner_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, c.ID, c.Slug, c.Name, c.Description, c.Layer, jsonText(nonNilStrings(c.Tags)), c.OwnerID, timeText(c.CreatedAt), timeText(c.UpdatedAt))
	return mapSQLError(err)
}

func (s *Store) UpdateComponent(ctx context.Context, c domain.Component) error {
	res, err := s.db.ExecContext(ctx, `UPDATE components SET slug=?,name=?,description=?,layer=?,tags_json=?,updated_at=? WHERE id=?`, c.Slug, c.Name, c.Description, c.Layer, jsonText(nonNilStrings(c.Tags)), timeText(c.UpdatedAt), c.ID)
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
	var tags string
	err := s.db.QueryRowContext(ctx, `SELECT id,slug,name,description,layer,tags_json,owner_id,created_at,updated_at FROM components WHERE id=?`, id).Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Layer, &tags, &c.OwnerID, &created, &updated)
	if err != nil {
		return c, mapSQLError(err)
	}
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	c.Tags = decodeJSON(tags, []string{})
	if includeReleases {
		c.Releases, err = s.ListComponentReleases(ctx, id, false)
	}
	return c, err
}

func (s *Store) ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error) {
	query := `SELECT id,slug,name,description,layer,tags_json,owner_id,created_at,updated_at FROM components`
	var args []any
	if viewer.Role == domain.RoleComponentOwner {
		query += ` WHERE owner_id=? OR EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND (r.status='released' OR (r.status='draft' AND r.candidate=1)))`
		args = append(args, viewer.ID)
	} else if viewer.Role != domain.RolePlatformAdmin {
		query += ` WHERE EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND (r.status='released' OR (r.status='draft' AND r.candidate=1)))`
	}
	query += ` ORDER BY CASE layer WHEN 'host_foundation' THEN 1 WHEN 'runtime_state' THEN 2 WHEN 'orchestration_core' THEN 3 WHEN 'cluster_service' THEN 4 WHEN 'observability_management' THEN 5 WHEN 'platform_extension' THEN 6 ELSE 7 END, name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Component
	for rows.Next() {
		var c domain.Component
		var cr, up, tags string
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Layer, &tags, &c.OwnerID, &cr, &up); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(cr)
		c.UpdatedAt = parseTime(up)
		c.Tags = decodeJSON(tags, []string{})
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	visible := make([]domain.Component, 0, len(out))
	for _, component := range out {
		ownerView := viewer.Role == domain.RolePlatformAdmin || (viewer.Role == domain.RoleComponentOwner && component.OwnerID == viewer.ID)
		component.Releases, err = s.listVisibleComponentReleases(ctx, component.ID, ownerView)
		if err != nil {
			return nil, err
		}
		if ownerView || len(component.Releases) > 0 {
			visible = append(visible, component)
		}
	}
	return visible, nil
}

func (s *Store) ComponentSlugExists(ctx context.Context, slug string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM components WHERE slug=?)`, slug).Scan(&exists)
	return exists != 0, err
}

func (s *Store) CreateComponentRelease(ctx context.Context, r domain.ComponentRelease) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertComponentRelease(ctx, tx, r); err != nil {
		return err
	}
	if r.Status == domain.ReleaseReleased {
		if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertComponentRelease(ctx context.Context, tx *sql.Tx, r domain.ComponentRelease) error {
	if err := validateReleaseCatalogTx(ctx, tx, r, nil); err != nil {
		return err
	}
	if r.ParentReleaseID == "" && r.LineID == "" {
		for _, action := range r.Actions {
			parentID := ""
			if action.Kind == domain.ActionUpgrade && action.ToReleaseID == r.ID {
				parentID = action.FromReleaseID
			} else if action.Kind == domain.ActionRollback && action.FromReleaseID == r.ID {
				parentID = action.ToReleaseID
			}
			if parentID == "" {
				continue
			}
			var componentID, lineID, lineName string
			if err := tx.QueryRowContext(ctx, `SELECT r.component_id,r.line_id,l.name FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id WHERE r.id=?`, parentID).Scan(&componentID, &lineID, &lineName); err == nil && componentID == r.ComponentID {
				r.ParentReleaseID, r.TemplateSourceReleaseID = parentID, parentID
				r.LineID, r.LineName = lineID, lineName
				break
			}
		}
	}
	if r.LineID == "" {
		r.LineID = "line-" + r.ID
	}
	if r.LineName == "" {
		r.LineName = r.Version
	}
	if r.Compatibility == "" {
		if r.ParentReleaseID != "" {
			r.Compatibility = domain.CompatibilityCompatible
		} else {
			r.Compatibility = domain.CompatibilityNotApplicable
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO component_release_lines(id,component_id,name,created_at) VALUES(?,?,?,?)`, r.LineID, r.ComponentID, r.LineName, timeText(r.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	if r.Review.Status == "" {
		r.Review.Status = domain.ReleaseReviewNotSubmitted
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO component_releases(id,component_id,line_id,parent_release_id,template_source_release_id,version,status,release_notes,compatibility,candidate,review_status,review_contract_digest,review_submitted_at,reviewed_by,reviewed_at,review_comment,risk_level,environment_constraints_json,parameters_json,created_at,released_at,deprecated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.ComponentID, r.LineID, nullString(r.ParentReleaseID), nullString(r.TemplateSourceReleaseID), r.Version, r.Status, r.ReleaseNotes, r.Compatibility, r.Candidate, r.Review.Status, r.Review.ContractDigest, ptrTimeText(r.Review.SubmittedAt), nullString(r.Review.ReviewedBy), ptrTimeText(r.Review.ReviewedAt), r.Review.Comment, r.RiskLevel, jsonText(r.EnvironmentConstraints), jsonText(r.Parameters), timeText(r.CreatedAt), ptrTimeText(r.ReleasedAt), ptrTimeText(r.DeprecatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err := replaceReleaseChildren(ctx, tx, r); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE component_releases SET candidate=?,review_status=?,review_contract_digest=?,review_submitted_at=?,reviewed_by=?,reviewed_at=?,review_comment=? WHERE id=?`, r.Candidate, r.Review.Status, r.Review.ContractDigest, ptrTimeText(r.Review.SubmittedAt), nullString(r.Review.ReviewedBy), ptrTimeText(r.Review.ReviewedAt), r.Review.Comment, r.ID)
	return err
}

// CreateClonedComponentRelease commits the cloned Release contract, artifacts,
// and audit record together. Managed Playbooks are prepared before this call
// and removed by the service if the transaction fails.
func (s *Store) CreateClonedComponentRelease(ctx context.Context, r domain.ComponentRelease, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertComponentRelease(ctx, tx, r); err != nil {
		return err
	}
	for _, artifact := range r.Artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_release_artifacts(id,release_id,alias,filename,sha256,size_bytes,source_url,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, artifact.ID, r.ID, artifact.Alias, artifact.Filename, artifact.SHA256, artifact.SizeBytes, artifact.SourceURL, artifact.SourceUpdatedBy, timeText(artifact.SourceUpdatedAt), artifact.CreatedBy, timeText(artifact.CreatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	for _, image := range r.Images {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_release_images(id,release_id,logical_name,digest,source_ref,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, image.ID, r.ID, image.LogicalName, image.Digest, image.SourceRef, image.SourceUpdatedBy, timeText(image.SourceUpdatedAt), image.CreatedBy, timeText(image.CreatedAt)); err != nil {
			return mapSQLError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	return tx.Commit()
}

func (s *Store) UpdateDraftRelease(ctx context.Context, r domain.ComponentRelease) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := getComponentRelease(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if err := validateReleaseCatalogTx(ctx, tx, r, &previous); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE component_releases SET version=?,release_notes=?,compatibility=?,candidate=0,review_status='not_submitted',review_contract_digest='',review_submitted_at=NULL,reviewed_by=NULL,reviewed_at=NULL,review_comment='',risk_level=?,environment_constraints_json=?,parameters_json=?,publication_generation=publication_generation+1 WHERE id=? AND status='draft'`, r.Version, r.ReleaseNotes, r.Compatibility, r.RiskLevel, jsonText(r.EnvironmentConstraints), jsonText(r.Parameters), r.ID)
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
		_, err := tx.ExecContext(ctx, `INSERT INTO action_definitions(id,release_id,name,kind,playbook,playbook_sha256,tags_json,host_group,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent,from_release_id,to_release_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ID, r.ID, a.Name, a.Kind, a.Playbook, a.PlaybookSHA256, jsonText(nonNilStrings(a.Tags)), a.HostGroup, jsonText(nonNilStrings(a.RequiredCredentials)), a.TimeoutSeconds, a.RiskLevel, a.Destructive, a.Idempotent, nullString(a.FromReleaseID), nullString(a.ToReleaseID))
		if err != nil {
			return mapSQLError(err)
		}
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	return getComponentRelease(ctx, s.db, id)
}

func getComponentRelease(ctx context.Context, q queryer, id string) (domain.ComponentRelease, error) {
	row := q.QueryRowContext(ctx, `SELECT r.id,r.component_id,r.line_id,l.name,r.parent_release_id,r.template_source_release_id,r.version,r.status,r.release_notes,r.compatibility,r.candidate,r.review_status,r.review_contract_digest,r.review_submitted_at,r.reviewed_by,r.reviewed_at,r.review_comment,r.publication_generation,r.risk_level,r.environment_constraints_json,r.parameters_json,r.created_at,r.released_at,r.deprecated_at FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id WHERE r.id=?`, id)
	r, err := scanRelease(row)
	if err != nil {
		return r, mapSQLError(err)
	}
	r.Dependencies, err = listDependencies(ctx, q, id)
	if err != nil {
		return r, err
	}
	r.Actions, err = listActions(ctx, q, id)
	if err != nil {
		return r, err
	}
	r.Artifacts, err = listComponentArtifacts(ctx, q, id)
	if err != nil {
		return r, err
	}
	r.Images, err = listComponentImages(ctx, q, id)
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
	var candidate int
	var constraints, parameters, created string
	var parent, template, submitted, reviewedBy, reviewed, released, deprecated sql.NullString
	err := row.Scan(&r.ID, &r.ComponentID, &r.LineID, &r.LineName, &parent, &template, &r.Version, &r.Status, &r.ReleaseNotes, &r.Compatibility, &candidate, &r.Review.Status, &r.Review.ContractDigest, &submitted, &reviewedBy, &reviewed, &r.Review.Comment, &r.PublicationGeneration, &r.RiskLevel, &constraints, &parameters, &created, &released, &deprecated)
	r.ParentReleaseID = parent.String
	r.TemplateSourceReleaseID = template.String
	r.Candidate = candidate != 0
	r.Review.SubmittedAt = parseNullTime(submitted)
	r.Review.ReviewedBy = reviewedBy.String
	r.Review.ReviewedAt = parseNullTime(reviewed)
	r.EnvironmentConstraints = decodeJSON(constraints, map[string]any{})
	r.Parameters = decodeJSON(parameters, []domain.ParameterDefinition{})
	r.CreatedAt = parseTime(created)
	r.ReleasedAt = parseNullTime(released)
	r.DeprecatedAt = parseNullTime(deprecated)
	return r, err
}

func (s *Store) ListComponentReleases(ctx context.Context, componentID string, releasedOnly bool) ([]domain.ComponentRelease, error) {
	q := `SELECT r.id,r.component_id,r.line_id,l.name,r.parent_release_id,r.template_source_release_id,r.version,r.status,r.release_notes,r.compatibility,r.candidate,r.review_status,r.review_contract_digest,r.review_submitted_at,r.reviewed_by,r.reviewed_at,r.review_comment,r.publication_generation,r.risk_level,r.environment_constraints_json,r.parameters_json,r.created_at,r.released_at,r.deprecated_at FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id WHERE r.component_id=?`
	if releasedOnly {
		q += ` AND r.status='released'`
	}
	q += ` ORDER BY r.created_at DESC`
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
		out[i].Artifacts, err = s.ListComponentArtifacts(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Images, err = s.ListComponentImages(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) listVisibleComponentReleases(ctx context.Context, componentID string, ownerView bool) ([]domain.ComponentRelease, error) {
	if ownerView {
		return s.ListComponentReleases(ctx, componentID, false)
	}
	q := `SELECT r.id,r.component_id,r.line_id,l.name,r.parent_release_id,r.template_source_release_id,r.version,r.status,r.release_notes,r.compatibility,r.candidate,r.review_status,r.review_contract_digest,r.review_submitted_at,r.reviewed_by,r.reviewed_at,r.review_comment,r.publication_generation,r.risk_level,r.environment_constraints_json,r.parameters_json,r.created_at,r.released_at,r.deprecated_at FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id WHERE r.component_id=? AND (r.status='released' OR (r.status='draft' AND r.candidate=1 AND r.review_status='approved')) ORDER BY r.created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, componentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ComponentRelease
	for rows.Next() {
		r, scanErr := scanRelease(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		r.Dependencies, err = s.listDependencies(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		r.Actions, err = s.listActions(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		r.Artifacts, err = s.ListComponentArtifacts(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		r.Images, err = s.ListComponentImages(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		if r.Status == domain.ReleaseReleased || r.IsApprovedCandidate() {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s *Store) listDependencies(ctx context.Context, releaseID string) ([]domain.ComponentDependency, error) {
	return listDependencies(ctx, s.db, releaseID)
}

func listDependencies(ctx context.Context, q queryer, releaseID string) ([]domain.ComponentDependency, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.id,d.release_id,d.upstream_component_id,d.upstream_release_id,d.purpose,d.parameter_mappings_json,c.name,ur.version FROM component_dependencies d JOIN components c ON c.id=d.upstream_component_id JOIN component_releases ur ON ur.id=d.upstream_release_id WHERE d.release_id=? ORDER BY c.name`, releaseID)
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
	return listActions(ctx, s.db, releaseID)
}

func listActions(ctx context.Context, q queryer, releaseID string) ([]domain.ActionDefinition, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,release_id,name,kind,playbook,playbook_sha256,tags_json,host_group,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent,from_release_id,to_release_id FROM action_definitions WHERE release_id=? ORDER BY kind,name`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ActionDefinition
	for rows.Next() {
		var a domain.ActionDefinition
		var tags, requiredCredentials string
		var destructive, idempotent int
		var from, to sql.NullString
		if err := rows.Scan(&a.ID, &a.ReleaseID, &a.Name, &a.Kind, &a.Playbook, &a.PlaybookSHA256, &tags, &a.HostGroup, &requiredCredentials, &a.TimeoutSeconds, &a.RiskLevel, &destructive, &idempotent, &from, &to); err != nil {
			return nil, err
		}
		a.Tags = nonNilStrings(decodeJSON(tags, []string{}))
		a.RequiredCredentials = nonNilStrings(decodeJSON(requiredCredentials, []string{}))
		a.Destructive = destructive != 0
		a.Idempotent = idempotent != 0
		a.FromReleaseID = from.String
		a.ToReleaseID = to.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeprecateComponentRelease(ctx context.Context, id string, at time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
UPDATE component_releases
SET status='deprecated',candidate=0,deprecated_at=?,publication_generation=publication_generation+1
WHERE id=?
  AND status IN ('draft','released')
	AND NOT EXISTS (
	  SELECT 1
	  FROM runs
	    JOIN json_each(runs.input_snapshot_json, '$.steps') AS step
	    WHERE runs.scenario_revision_id IS NOT NULL
	    AND runs.kind IN ('scenario_test','scenario_run')
	    AND json_extract(step.value, '$.releaseId')=component_releases.id
	    AND component_releases.status='released'
	)`, timeText(at), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_ = tx.Rollback()
		var status domain.ReleaseStatus
		if statusErr := s.db.QueryRowContext(ctx, `SELECT status FROM component_releases WHERE id=?`, id).Scan(&status); statusErr != nil {
			return mapSQLError(statusErr)
		}
		if status == domain.ReleaseReleased {
			if count, countErr := s.CountScenarioRunsForComponentRelease(ctx, id); countErr != nil {
				return countErr
			} else if count > 0 {
				return fmt.Errorf("%w: component release is retained by %d scenario run(s)", domain.ErrConflict, count)
			}
		}
		return fmt.Errorf("%w: only draft or released versions can be deprecated", domain.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreDeprecatedComponentRelease returns a never-published deprecated
// Release to Draft. The checks mirror the partial unique indexes so callers
// receive an actionable conflict instead of a raw SQLite constraint error.
func (s *Store) RestoreDeprecatedComponentRelease(ctx context.Context, id string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status domain.ReleaseStatus
	var lineID string
	var parentReleaseID sql.NullString
	var releasedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,line_id,parent_release_id,released_at FROM component_releases WHERE id=?`, id).Scan(&status, &lineID, &parentReleaseID, &releasedAt); err != nil {
		return mapSQLError(err)
	}
	if status != domain.ReleaseDeprecated || releasedAt.Valid {
		return fmt.Errorf("%w: only never-published deprecated releases can be restored", domain.ErrConflict)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_releases WHERE line_id=? AND id<>? AND status='draft'`, lineID, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: release line already has an active draft", domain.ErrConflict)
	}
	if parentReleaseID.Valid {
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM component_releases
WHERE parent_release_id=? AND id<>?
  AND (status IN ('draft','released') OR released_at IS NOT NULL)`, parentReleaseID.String, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("%w: parent release already has another successor", domain.ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE component_releases
SET status='draft',candidate=0,deprecated_at=NULL,publication_generation=publication_generation+1
WHERE id=? AND status='deprecated' AND released_at IS NULL`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: release lifecycle changed; refresh and retry", domain.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

type ComponentReleaseDeletionImpact struct {
	RunCount                  int
	ImageBuildCount           int
	DownstreamDependencyCount int
	ScenarioReferenceCount    int
	ReleaseReferenceCount     int
	ActionReferenceCount      int
	InstallationCount         int
}

func (impact ComponentReleaseDeletionImpact) ReferenceCount() int {
	return impact.DownstreamDependencyCount + impact.ScenarioReferenceCount + impact.ReleaseReferenceCount + impact.ActionReferenceCount + impact.InstallationCount
}

func componentReleaseDeletionImpact(ctx context.Context, q queryer, id string) (ComponentReleaseDeletionImpact, error) {
	var impact ComponentReleaseDeletionImpact
	err := q.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(DISTINCT r.id) FROM runs r
   WHERE r.component_release_id=?
      OR (r.kind IN ('scenario_test','scenario_run') AND EXISTS (
        SELECT 1 FROM json_each(r.input_snapshot_json, '$.steps') step
        WHERE json_extract(step.value, '$.releaseId')=?
      ))),
  (SELECT COUNT(*) FROM component_image_builds WHERE release_id=?),
  (SELECT COUNT(*) FROM component_dependencies WHERE upstream_release_id=?),
  (SELECT COUNT(*) FROM scenario_revisions sr
   WHERE EXISTS (
     SELECT 1 FROM json_each(sr.graph_json, '$.nodes') node
     WHERE json_extract(node.value, '$.releaseId')=?
   )),
  (SELECT COUNT(*) FROM component_releases
   WHERE id<>? AND (parent_release_id=? OR template_source_release_id=?)),
  (SELECT COUNT(*) FROM action_definitions
   WHERE release_id<>? AND (from_release_id=? OR to_release_id=?)),
  (SELECT COUNT(*) FROM environment_component_installations WHERE release_id=?)`,
		id, id, id, id, id, id, id, id, id, id, id, id,
	).Scan(
		&impact.RunCount,
		&impact.ImageBuildCount,
		&impact.DownstreamDependencyCount,
		&impact.ScenarioReferenceCount,
		&impact.ReleaseReferenceCount,
		&impact.ActionReferenceCount,
		&impact.InstallationCount,
	)
	return impact, err
}

func (s *Store) ComponentReleaseDeletionImpact(ctx context.Context, id string) (ComponentReleaseDeletionImpact, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_releases WHERE id=?`, id).Scan(&exists); err != nil {
		return ComponentReleaseDeletionImpact{}, err
	}
	if exists == 0 {
		return ComponentReleaseDeletionImpact{}, domain.ErrNotFound
	}
	return componentReleaseDeletionImpact(ctx, s.db, id)
}

// DeleteComponentRelease permanently removes only a never-published Release
// that has already been deprecated and has no retained execution or reference
// evidence. Owned Draft content is cascaded; the audit event remains.
func (s *Store) DeleteComponentRelease(ctx context.Context, id string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status domain.ReleaseStatus
	var lineID string
	var releasedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,line_id,released_at FROM component_releases WHERE id=?`, id).Scan(&status, &lineID, &releasedAt); err != nil {
		return mapSQLError(err)
	}
	if status != domain.ReleaseDeprecated || releasedAt.Valid {
		return fmt.Errorf("%w: only never-published deprecated releases can be permanently deleted", domain.ErrConflict)
	}
	impact, err := componentReleaseDeletionImpact(ctx, tx, id)
	if err != nil {
		return err
	}
	if impact.RunCount > 0 {
		return fmt.Errorf("%w: component release run history must be retained", domain.ErrConflict)
	}
	if impact.ImageBuildCount > 0 {
		return fmt.Errorf("%w: component release image build history must be retained", domain.ErrConflict)
	}
	if impact.ReferenceCount() > 0 {
		return fmt.Errorf("%w: component release is still referenced", domain.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM component_releases WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM component_release_lines WHERE id=? AND NOT EXISTS (SELECT 1 FROM component_releases WHERE line_id=?)`, lineID, lineID); err != nil {
		return mapSQLError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}
func invalidateDraftReleaseDeliveryTx(ctx context.Context, tx *sql.Tx, id string) error {
	var status domain.ReleaseStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM component_releases WHERE id=?`, id).Scan(&status); err != nil {
		return mapSQLError(err)
	}
	if status != domain.ReleaseDraft {
		return fmt.Errorf("%w: release is no longer a mutable draft", domain.ErrConflict)
	}
	_, err := tx.ExecContext(ctx, `UPDATE component_releases SET candidate=0,review_status='not_submitted',review_contract_digest='',review_submitted_at=NULL,reviewed_by=NULL,reviewed_at=NULL,review_comment='',publication_generation=publication_generation+1 WHERE id=?`, id)
	return err
}

func (s *Store) InvalidateDraftReleaseDelivery(ctx context.Context, id string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateDraftActionPlaybookDigest changes the immutable-content identity for
// every action that references a managed Playbook. Updating the digest before
// publishing the staged file fails closed: an interrupted write can only make
// old evidence inapplicable, never make changed executable content look ready.
func (s *Store) UpdateDraftActionPlaybookDigest(ctx context.Context, releaseID, playbook, sha256 string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateDraftReleaseDeliveryTx(ctx, tx, releaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE action_definitions SET playbook_sha256=? WHERE release_id=? AND playbook=?`, sha256, releaseID, playbook); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetReleaseCandidate(ctx context.Context, id string, candidate bool, expectedGeneration int64, expectedDigest string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	release, err := getComponentRelease(ctx, tx, id)
	if err != nil {
		return err
	}
	if release.PublicationGeneration != expectedGeneration || domain.ComponentReleaseSpecDigest(release) != expectedDigest ||
		(candidate && (release.Review.Status != domain.ReleaseReviewApproved || release.Review.ContractDigest != expectedDigest)) {
		return fmt.Errorf("%w: release or approval changed; refresh and retry", domain.ErrConflict)
	}
	res, err := tx.ExecContext(ctx, `UPDATE component_releases SET candidate=?,publication_generation=publication_generation+1 WHERE id=? AND status='draft' AND publication_generation=?`, candidate, id, expectedGeneration)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return fmt.Errorf("%w: only a draft release can change candidate state", domain.ErrConflict)
	}
	return tx.Commit()
}

func (s *Store) SubmitComponentReleaseReview(ctx context.Context, id, digest string, expectedGeneration int64, submittedAt time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	release, err := getComponentRelease(ctx, tx, id)
	if err != nil {
		return err
	}
	if release.PublicationGeneration != expectedGeneration || domain.ComponentReleaseSpecDigest(release) != digest {
		return fmt.Errorf("%w: release changed; refresh and retry", domain.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `UPDATE component_releases SET review_status='pending',review_contract_digest=?,review_submitted_at=?,reviewed_by=NULL,reviewed_at=NULL,review_comment='',candidate=0,publication_generation=publication_generation+1 WHERE id=? AND status='draft' AND publication_generation=?`, digest, timeText(submittedAt), id, expectedGeneration)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: only a draft release can be submitted for review", domain.ErrConflict)
	}
	return tx.Commit()
}

func (s *Store) DecideComponentReleaseReview(ctx context.Context, id string, status domain.ReleaseReviewStatus, reviewer, comment, digest string, reviewedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE component_releases SET review_status=?,reviewed_by=?,reviewed_at=?,review_comment=?,candidate=0,publication_generation=publication_generation+1 WHERE id=? AND status='draft' AND review_status='pending' AND review_contract_digest=?`, status, reviewer, timeText(reviewedAt), comment, id, digest)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: release review is no longer pending for this contract", domain.ErrConflict)
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

func (s *Store) LatestReleasedInLine(ctx context.Context, lineID string) (domain.ComponentRelease, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM component_releases WHERE line_id=? AND status='released' ORDER BY released_at DESC,created_at DESC LIMIT 1`, lineID).Scan(&id)
	if err != nil {
		return domain.ComponentRelease{}, mapSQLError(err)
	}
	return s.GetComponentRelease(ctx, id)
}

func (s *Store) LatestPublishedInLine(ctx context.Context, lineID string) (domain.ComponentRelease, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM component_releases WHERE line_id=? AND released_at IS NOT NULL ORDER BY released_at DESC,created_at DESC LIMIT 1`, lineID).Scan(&id)
	if err != nil {
		return domain.ComponentRelease{}, mapSQLError(err)
	}
	return s.GetComponentRelease(ctx, id)
}

func (s *Store) HasRetainedSuccessor(ctx context.Context, parentReleaseID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM component_releases
		WHERE parent_release_id=? AND (status IN ('draft','released') OR released_at IS NOT NULL)
	)`, parentReleaseID).Scan(&exists)
	return exists != 0, err
}

func (s *Store) HasActiveDraftInLine(ctx context.Context, lineID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM component_releases WHERE line_id=? AND status='draft')`, lineID).Scan(&exists)
	return exists != 0, err
}

func (s *Store) GetReleaseLine(ctx context.Context, lineID string) (domain.ComponentReleaseLine, error) {
	var line domain.ComponentReleaseLine
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,component_id,name,created_at FROM component_release_lines WHERE id=?`, lineID).Scan(&line.ID, &line.ComponentID, &line.Name, &created)
	if err != nil {
		return line, mapSQLError(err)
	}
	line.CreatedAt = parseTime(created)
	return line, nil
}

func (s *Store) RenameReleaseLine(ctx context.Context, lineID, componentID, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE component_release_lines SET name=? WHERE id=? AND component_id=?`, name, lineID, componentID)
	if err != nil {
		return mapSQLError(err)
	}
	updated, _ := res.RowsAffected()
	if updated == 0 {
		return domain.ErrNotFound
	}
	return nil
}
