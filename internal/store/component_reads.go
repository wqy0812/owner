package store

import (
	"context"
	"database/sql"
	"strings"

	"codex/platform-demo/internal/domain"
)

const releaseSelect = `SELECT r.id,r.component_id,r.line_id,l.name,r.parent_release_id,r.template_source_release_id,r.version,r.status,r.release_notes,r.compatibility,r.candidate,r.review_status,r.review_contract_digest,r.review_submitted_at,r.reviewed_by,r.reviewed_at,r.review_comment,r.publication_generation,r.risk_level,r.environment_constraints_json,r.parameters_json,r.playbook_tree_sha256,r.playbook_workspace_root,r.created_at,r.released_at,r.deprecated_at FROM component_releases r JOIN component_release_lines l ON l.id=r.line_id`

// Lists hydrate a complete contract within one read snapshot. No write lock or
// cross-request cache is needed, and child cursors never hold a second connection.
func (s *Store) ListComponents(ctx context.Context, viewer domain.User) ([]domain.Component, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	items, err := listComponents(ctx, tx, viewer)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func listComponents(ctx context.Context, q queryer, viewer domain.User) ([]domain.Component, error) {
	query := `SELECT id,slug,name,description,layer,tags_json,owner_id,created_at,updated_at FROM components`
	var args []any
	if viewer.Role == domain.RoleComponentOwner {
		query += ` WHERE owner_id=? OR EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND (r.status='released' OR (r.status='draft' AND r.candidate=1)))`
		args = append(args, viewer.ID)
	} else if viewer.Role != domain.RolePlatformAdmin {
		query += ` WHERE EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=components.id AND (r.status='released' OR (r.status='draft' AND r.candidate=1)))`
	}
	query += ` ORDER BY CASE layer WHEN 'host_foundation' THEN 1 WHEN 'runtime_state' THEN 2 WHEN 'orchestration_core' THEN 3 WHEN 'cluster_service' THEN 4 WHEN 'observability_management' THEN 5 WHEN 'platform_extension' THEN 6 ELSE 7 END, name`
	rows, err := q.QueryContext(ctx, query, args...)
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
	if len(out) == 0 {
		return []domain.Component{}, nil
	}
	releaseQuery := releaseSelect + ` JOIN components c ON c.id=r.component_id`
	var releaseArgs []any
	if viewer.Role != domain.RolePlatformAdmin {
		releaseQuery += ` WHERE (r.status='released' OR (r.status='draft' AND r.candidate=1 AND r.review_status='approved'))`
		if viewer.Role == domain.RoleComponentOwner {
			releaseQuery += ` OR c.owner_id=?`
			releaseArgs = append(releaseArgs, viewer.ID)
		}
	}
	releases, err := listReleases(ctx, q, releaseQuery+` ORDER BY r.created_at DESC,r.id DESC`, releaseArgs...)
	if err != nil {
		return nil, err
	}
	byComponent := make(map[string][]domain.ComponentRelease, len(out))
	for _, release := range releases {
		byComponent[release.ComponentID] = append(byComponent[release.ComponentID], release)
	}
	visible := make([]domain.Component, 0, len(out))
	for _, component := range out {
		ownerView := viewer.Role == domain.RolePlatformAdmin || (viewer.Role == domain.RoleComponentOwner && component.OwnerID == viewer.ID)
		for _, release := range byComponent[component.ID] {
			// Approval is bound to the fully loaded current contract, never just
			// to the candidate flag or the persisted review status.
			if ownerView || release.Status == domain.ReleaseReleased || release.IsApprovedCandidate() {
				component.Releases = append(component.Releases, release)
			}
		}
		if ownerView || len(component.Releases) > 0 {
			visible = append(visible, component)
		}
	}
	return visible, nil
}

func (s *Store) ListComponentReleases(ctx context.Context, componentID string, releasedOnly bool) ([]domain.ComponentRelease, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := releaseSelect + ` WHERE r.component_id=?`
	if releasedOnly {
		query += ` AND r.status='released'`
	}
	items, err := listReleases(ctx, tx, query+` ORDER BY r.created_at DESC,r.id DESC`, componentID)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func listReleases(ctx context.Context, q queryer, query string, args ...any) ([]domain.ComponentRelease, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var releases []domain.ComponentRelease
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := hydrateReleaseChildren(ctx, q, releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// Bound parameter counts even for large catalogs. Each batch uses four child
// queries, retaining each child's ordering and the existing empty-array shape.
const releaseReadBatchSize = 256

func hydrateReleaseChildren(ctx context.Context, q queryer, releases []domain.ComponentRelease) error {
	for start := 0; start < len(releases); start += releaseReadBatchSize {
		batch := releases[start:min(start+releaseReadBatchSize, len(releases))]
		ids := make([]string, 0, len(batch))
		byID := make(map[string]*domain.ComponentRelease, len(batch))
		for i := range batch {
			r := &batch[i]
			ids = append(ids, r.ID)
			byID[r.ID] = r
			r.PlaybookFiles = []domain.ComponentPlaybookFile{}
			r.Artifacts = []domain.ComponentArtifact{}
			r.Images = []domain.ComponentImage{}
		}
		dependencies, err := listDependencies(ctx, q, ids...)
		if err != nil {
			return err
		}
		for _, item := range dependencies {
			r := byID[item.ReleaseID]
			r.Dependencies = append(r.Dependencies, item)
		}
		actions, err := listActions(ctx, q, ids...)
		if err != nil {
			return err
		}
		for _, item := range actions {
			r := byID[item.ReleaseID]
			r.Actions = append(r.Actions, item)
		}
		playbookFiles, err := listComponentPlaybookFiles(ctx, q, ids...)
		if err != nil {
			return err
		}
		for _, item := range playbookFiles {
			r := byID[item.ReleaseID]
			r.PlaybookFiles = append(r.PlaybookFiles, item)
		}
		artifacts, err := listComponentArtifacts(ctx, q, ids...)
		if err != nil {
			return err
		}
		for _, item := range artifacts {
			r := byID[item.ReleaseID]
			r.Artifacts = append(r.Artifacts, item)
		}
		images, err := listComponentImages(ctx, q, ids...)
		if err != nil {
			return err
		}
		for _, item := range images {
			r := byID[item.ReleaseID]
			r.Images = append(r.Images, item)
		}
	}
	return nil
}

func releaseIDPlaceholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if len(ids) == 0 {
		return "NULL", args
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}
