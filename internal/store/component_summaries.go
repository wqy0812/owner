package store

import (
	"context"
	"database/sql"

	"codex/platform-demo/internal/domain"
)

// ListComponentSummaries reads directory metadata in one snapshot. Only shared
// candidate Drafts outside the viewer's ownership require full DB contracts;
// ordinary releases never hydrate their parameters, files or delivery assets.
func (s *Store) ListComponentSummaries(ctx context.Context, viewer domain.User) ([]domain.ComponentSummary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	items, err := listComponentSummaries(ctx, tx, viewer)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

type componentDirectoryRelease struct {
	id                    string
	componentID           string
	status                domain.ReleaseStatus
	hasMappings           bool
	requiresApprovalCheck bool
}

func listComponentSummaries(ctx context.Context, q queryer, viewer domain.User) ([]domain.ComponentSummary, error) {
	items := []domain.ComponentSummary{}
	if !viewer.Role.Valid() {
		return items, nil
	}
	query := `SELECT c.id,c.slug,c.name,c.description,c.layer,c.tags_json,c.owner_id,COALESCE(u.name,''),c.created_at,c.updated_at
FROM components c LEFT JOIN users u ON u.id=c.owner_id`
	var args []any
	if viewer.Role != domain.RolePlatformAdmin {
		query += ` WHERE (EXISTS (SELECT 1 FROM component_releases r WHERE r.component_id=c.id AND (r.status='released' OR (r.status='draft' AND r.candidate=1 AND r.review_status='approved')))`
		if viewer.Role == domain.RoleComponentOwner {
			query += ` OR c.owner_id=?`
			args = append(args, viewer.ID)
		}
		query += `)`
	}
	query += ` ORDER BY CASE c.layer WHEN 'host_foundation' THEN 1 WHEN 'runtime_state' THEN 2 WHEN 'orchestration_core' THEN 3 WHEN 'cluster_service' THEN 4 WHEN 'observability_management' THEN 5 WHEN 'platform_extension' THEN 6 ELSE 7 END,c.name`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]int{}
	ownerView := map[string]bool{}
	for rows.Next() {
		var item domain.ComponentSummary
		var tags, created, updated string
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.Layer, &tags, &item.OwnerID, &item.OwnerName, &created, &updated); err != nil {
			return nil, err
		}
		item.Tags = decodeJSON(tags, []string{})
		item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
		byID[item.ID] = len(items)
		ownerView[item.ID] = viewer.Role == domain.RolePlatformAdmin || (viewer.Role == domain.RoleComponentOwner && item.OwnerID == viewer.ID)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}

	// The mapping-first selection needs only existence, not mapping contents.
	query = `SELECT r.id,r.component_id,r.status,
EXISTS (SELECT 1 FROM component_dependencies d WHERE d.release_id=r.id AND json_array_length(d.parameter_mappings_json)>0)
FROM component_releases r JOIN components c ON c.id=r.component_id`
	args = nil
	if viewer.Role != domain.RolePlatformAdmin {
		query += ` WHERE (r.status='released' OR (r.status='draft' AND r.candidate=1 AND r.review_status='approved')`
		if viewer.Role == domain.RoleComponentOwner {
			query += ` OR c.owner_id=?`
			args = append(args, viewer.ID)
		}
		query += `)`
	}
	query += ` ORDER BY r.created_at DESC,r.id DESC`
	releaseRows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer releaseRows.Close()
	releases := []componentDirectoryRelease{}
	candidateIDs := []string{}
	for releaseRows.Next() {
		var release componentDirectoryRelease
		if err := releaseRows.Scan(&release.id, &release.componentID, &release.status, &release.hasMappings); err != nil {
			return nil, err
		}
		if _, exists := byID[release.componentID]; !exists {
			continue
		}
		release.requiresApprovalCheck = !ownerView[release.componentID] && release.status != domain.ReleaseReleased
		if release.requiresApprovalCheck {
			candidateIDs = append(candidateIDs, release.id)
		}
		releases = append(releases, release)
	}
	if err := releaseRows.Err(); err != nil {
		return nil, err
	}
	if err := releaseRows.Close(); err != nil {
		return nil, err
	}

	// Do not substitute persisted approval flags for the current contract digest.
	// Hydrate only these candidates, in bounded batches in the same snapshot.
	approved := make(map[string]bool, len(candidateIDs))
	for start := 0; start < len(candidateIDs); start += releaseReadBatchSize {
		batch := candidateIDs[start:min(start+releaseReadBatchSize, len(candidateIDs))]
		placeholders, candidateArgs := releaseIDPlaceholders(batch)
		candidates, err := listReleases(ctx, q, releaseSelect+` WHERE r.id IN (`+placeholders+`)`, candidateArgs...)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			approved[candidate.ID] = candidate.IsApprovedCandidate()
		}
	}
	mappingSelected := map[string]bool{}
	for _, release := range releases {
		if release.requiresApprovalCheck && !approved[release.id] {
			continue
		}
		item := &items[byID[release.componentID]]
		item.ReleaseCount++
		item.HasDraft = item.HasDraft || release.status == domain.ReleaseDraft
		if item.DefaultReleaseID == "" {
			item.DefaultReleaseID = release.id
		}
		if release.hasMappings && !mappingSelected[item.ID] {
			item.DefaultReleaseID = release.id
			mappingSelected[item.ID] = true
		}
	}
	visible := make([]domain.ComponentSummary, 0, len(items))
	for _, item := range items {
		if !ownerView[item.ID] && item.ReleaseCount == 0 {
			continue
		}
		item.NeedsAttention = item.ReleaseCount == 0 || item.HasDraft
		visible = append(visible, item)
	}
	return visible, nil
}
