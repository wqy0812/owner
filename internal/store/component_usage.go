package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"fmt"
)

type ComponentUsageRelease struct {
	ComponentID       string `json:"componentId"`
	Name              string `json:"name"`
	ReleaseID         string `json:"releaseId"`
	Version           string `json:"version"`
	LineName          string `json:"lineName"`
	Status            string `json:"status"`
	OwnerID           string `json:"ownerId"`
	OwnerName         string `json:"ownerName"`
	UpstreamReleaseID string `json:"upstreamReleaseId"`
	UpstreamVersion   string `json:"upstreamVersion"`
	DependencyKind    string `json:"dependencyKind"`
	CanViewDetails    bool   `json:"canViewDetails"`
}
type ComponentUsageScenario struct {
	ScenarioID     string `json:"scenarioId"`
	Name           string `json:"name"`
	RevisionID     string `json:"revisionId"`
	Revision       int    `json:"revision"`
	Status         string `json:"status"`
	OwnerID        string `json:"ownerId"`
	OwnerName      string `json:"ownerName"`
	ReleaseID      string `json:"releaseId"`
	Version        string `json:"version"`
	Current        bool   `json:"current"`
	References     int    `json:"references"`
	CanViewDetails bool   `json:"canViewDetails"`
}
type ComponentUsage struct {
	Components     []ComponentUsageRelease  `json:"components"`
	Scenarios      []ComponentUsageScenario `json:"scenarios"`
	ComponentCount int                      `json:"componentCount"`
	ScenarioCount  int                      `json:"scenarioCount"`
}

// Deliberate summary projection: private contracts, parameters and graph contents
// never cross this boundary. All relationship reads share a single snapshot.
func (s *Store) ComponentUsage(ctx context.Context, viewer domain.User, id, releaseID string, history bool) (ComponentUsage, error) {
	out := ComponentUsage{Components: []ComponentUsageRelease{}, Scenarios: []ComponentUsageScenario{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT owner_id FROM components WHERE id=?`, id).Scan(&owner); err != nil {
		return out, mapSQLError(err)
	}
	if viewer.Role != domain.RolePlatformAdmin && !(viewer.Role == domain.RoleComponentOwner && viewer.ID == owner) {
		return out, domain.ErrForbidden
	}
	if releaseID != "" {
		var found int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_releases WHERE id=? AND component_id=?`, releaseID, id).Scan(&found); err != nil {
			return out, err
		}
		if found != 1 {
			return out, fmt.Errorf("%w: 指定版本不属于当前组件", domain.ErrInvalid)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.name,r.id,r.version,l.name,r.status,c.owner_id,u.name,up.id,up.version,d.kind FROM component_dependencies d JOIN component_releases r ON r.id=d.release_id JOIN components c ON c.id=r.component_id JOIN users u ON u.id=c.owner_id JOIN component_release_lines l ON l.id=r.line_id JOIN component_releases up ON up.id=d.upstream_release_id WHERE d.upstream_component_id=? AND (?='' OR up.id=?) AND (? OR r.status<>'deprecated') ORDER BY c.name,r.version,r.id`, id, releaseID, releaseID, history)
	if err != nil {
		return out, err
	}
	components := map[string]bool{}
	for rows.Next() {
		var row ComponentUsageRelease
		if err = rows.Scan(&row.ComponentID, &row.Name, &row.ReleaseID, &row.Version, &row.LineName, &row.Status, &row.OwnerID, &row.OwnerName, &row.UpstreamReleaseID, &row.UpstreamVersion, &row.DependencyKind); err != nil {
			rows.Close()
			return out, err
		}
		if row.DependencyKind == "" {
			row.DependencyKind = "execution"
		}
		row.CanViewDetails = viewer.Role == domain.RolePlatformAdmin || viewer.ID == row.OwnerID || row.Status == "released"
		out.Components = append(out.Components, row)
		components[row.ComponentID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Digest-bound candidate approval uses the established predicate. Candidate
	// contract loading is limited to the exact candidate that needs a detail link.
	for i := range out.Components {
		r := &out.Components[i]
		if !r.CanViewDetails && r.Status == "draft" {
			release, e := getComponentRelease(ctx, tx, r.ReleaseID)
			if e != nil {
				return out, e
			}
			r.CanViewDetails = release.IsApprovedCandidate()
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT s.id,s.name,sr.id,sr.revision,CASE WHEN sr.abandoned_at IS NOT NULL THEN 'abandoned' ELSE sr.status END,s.owner_id,u.name,r.id,r.version,s.current_revision_id=sr.id,COUNT(*) FROM scenario_revisions sr JOIN scenarios s ON s.id=sr.scenario_id JOIN users u ON u.id=s.owner_id JOIN json_each(sr.graph_json,'$.nodes') node JOIN component_releases r ON r.id=json_extract(node.value,'$.releaseId') WHERE r.component_id=? AND (?='' OR r.id=?) AND (? OR (s.current_revision_id=sr.id AND sr.status<>'deprecated' AND sr.abandoned_at IS NULL)) GROUP BY s.id,sr.id,r.id ORDER BY s.name,sr.revision DESC,r.version`, id, releaseID, releaseID, history)
	if err != nil {
		return out, err
	}
	scenarios := map[string]bool{}
	for rows.Next() {
		var row ComponentUsageScenario
		if err = rows.Scan(&row.ScenarioID, &row.Name, &row.RevisionID, &row.Revision, &row.Status, &row.OwnerID, &row.OwnerName, &row.ReleaseID, &row.Version, &row.Current, &row.References); err != nil {
			rows.Close()
			return out, err
		}
		row.CanViewDetails = viewer.Role == domain.RolePlatformAdmin || (viewer.Role == domain.RoleScenarioOwner && viewer.ID == row.OwnerID) || row.Status == "released"
		out.Scenarios = append(out.Scenarios, row)
		scenarios[row.ScenarioID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.ComponentCount = len(components)
	out.ScenarioCount = len(scenarios)
	return out, tx.Commit()
}
