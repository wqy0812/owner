package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

// RunSummary deliberately excludes execution snapshots, steps and logs.
type RunSummary struct {
	ArchiveStatus      string            `json:"archiveStatus"`
	ArchivedAt         *time.Time        `json:"archivedAt,omitempty"`
	ArchiveSizeBytes   int64             `json:"archiveSizeBytes"`
	ID                 string            `json:"id"`
	Kind               domain.RunKind    `json:"kind"`
	Status             domain.RunStatus  `json:"status"`
	Name               string            `json:"name"`
	EnvironmentID      string            `json:"environmentId"`
	EnvironmentName    string            `json:"environmentName"`
	RequestedBy        string            `json:"requestedBy"`
	CreatedByName      string            `json:"createdByName"`
	ComponentReleaseID string            `json:"componentReleaseId,omitempty"`
	ComponentID        string            `json:"componentId,omitempty"`
	ComponentName      string            `json:"componentName,omitempty"`
	ScenarioID         string            `json:"scenarioId,omitempty"`
	ScenarioName       string            `json:"scenarioName,omitempty"`
	Action             domain.ActionKind `json:"action"`
	CreatedAt          time.Time         `json:"createdAt"`
	StartedAt          *time.Time        `json:"startedAt,omitempty"`
	FinishedAt         *time.Time        `json:"finishedAt,omitempty"`
	QueuePosition      int               `json:"queuePosition,omitempty"`
	ApprovalID         string            `json:"approvalId,omitempty"`
}

type RunPage struct {
	Items    []RunSummary `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
	Total    int          `json:"total"`
}

type RunListOptions struct {
	Page, PageSize        int
	Filter, EnvironmentID string
	Archive               string
}

// Keep this predicate shared with the full internal read used by Workbench.
func runVisibility(viewer domain.User) (string, []any) {
	switch viewer.Role {
	case domain.RoleEnvironmentOwner:
		return `EXISTS (SELECT 1 FROM environments e WHERE e.id=runs.environment_id AND e.owner_id=?)`, []any{viewer.ID}
	case domain.RoleComponentOwner:
		return `(requested_by=? OR EXISTS (SELECT 1 FROM component_releases cr JOIN components c ON c.id=cr.component_id WHERE cr.id=runs.component_release_id AND c.owner_id=?) OR EXISTS (SELECT 1 FROM json_each(runs.input_snapshot_json,'$.steps') step JOIN component_releases cr ON cr.id=json_extract(step.value,'$.releaseId') JOIN components c ON c.id=cr.component_id WHERE c.owner_id=?))`, []any{viewer.ID, viewer.ID, viewer.ID}
	case domain.RoleScenarioOwner:
		return `(requested_by=? OR EXISTS (SELECT 1 FROM scenario_revisions sr JOIN scenarios s ON s.id=sr.scenario_id WHERE sr.id=runs.scenario_revision_id AND s.owner_id=?))`, []any{viewer.ID, viewer.ID}
	case domain.RolePlatformAdmin:
		return `1=1`, nil
	default:
		return `1=0`, nil
	}
}

func (s *Store) ListRunSummaries(ctx context.Context, viewer domain.User, options RunListOptions) (RunPage, error) {
	result := RunPage{Items: []RunSummary{}, Page: options.Page, PageSize: options.PageSize}
	if options.Page < 1 || options.Page > 1000000 || options.PageSize < 1 || options.PageSize > 100 {
		return result, fmt.Errorf("%w: invalid pagination", domain.ErrInvalid)
	}
	where, args := runVisibility(viewer)
	switch options.Archive {
	case "", "unarchived":
		where += ` AND NOT EXISTS(SELECT 1 FROM run_archive_files af WHERE af.run_id=runs.id)`
	case "archived":
		where += ` AND EXISTS(SELECT 1 FROM run_archive_files af WHERE af.run_id=runs.id)`
	case "all":
	default:
		return result, fmt.Errorf("%w: invalid archive filter", domain.ErrInvalid)
	}
	switch options.Filter {
	case "", "all":
	case "active":
		where += ` AND runs.status IN ('queued','awaiting_approval','running')`
	case "finished":
		where += ` AND runs.status NOT IN ('queued','awaiting_approval','running')`
	default:
		return result, fmt.Errorf("%w: invalid run filter", domain.ErrInvalid)
	}
	if options.EnvironmentID != "" {
		where += ` AND runs.environment_id=?`
		args = append(args, options.EnvironmentID)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE `+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	result.Items, err = readRunSummaries(ctx, tx, where, args, options.PageSize, (options.Page-1)*options.PageSize)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// Only pending, owned approvals requiring no per-item delivery choice qualify.
func (s *Store) BatchApprovalCandidates(ctx context.Context, viewer domain.User) ([]RunSummary, error) {
	if viewer.Role != domain.RoleEnvironmentOwner {
		return nil, domain.ErrForbidden
	}
	where, args := runVisibility(viewer)
	where += ` AND runs.status='awaiting_approval' AND EXISTS (SELECT 1 FROM approvals a WHERE a.run_id=runs.id AND a.status='pending') AND COALESCE(json_array_length(runs.input_snapshot_json,'$.deliveryRequirements'),0)=0`
	return readRunSummaries(ctx, s.db, where, args, 0, 0)
}

func (s *Store) ReleaseRunSummaries(ctx context.Context, viewer domain.User, releaseID string) ([]RunSummary, error) {
	where, args := runVisibility(viewer)
	where += ` AND ((runs.kind='component_test' AND runs.component_release_id=?) OR (runs.kind IN ('scenario_test','scenario_run') AND EXISTS (SELECT 1 FROM json_each(runs.input_snapshot_json,'$.steps') locked_step WHERE json_extract(locked_step.value,'$.releaseId')=?)))`
	args = append(args, releaseID, releaseID)
	return readRunSummaries(ctx, s.db, where, args, 0, 0)
}

func readRunSummaries(ctx context.Context, q queryer, where string, args []any, limit, offset int) ([]RunSummary, error) {
	// Resolve names by joins, never through runDTO or per-row Store reads.
	query := `SELECT COALESCE((SELECT status FROM run_archive_tasks WHERE run_id=runs.id),'unarchived'),(SELECT archived_at FROM run_archive_files WHERE run_id=runs.id),COALESCE((SELECT size_bytes FROM run_archive_files WHERE run_id=runs.id),0),runs.id,runs.kind,runs.status,runs.environment_id,COALESCE(e.name,''),runs.requested_by,COALESCE(u.name,''),COALESCE(runs.component_release_id,''),COALESCE(c.id,''),COALESCE(c.name,''),COALESCE(cr.version,''),COALESCE(sc.id,''),COALESCE(sc.name,''),runs.action_kind,runs.created_at,runs.started_at,runs.finished_at,COALESCE(a.id,''),CASE WHEN runs.status='queued' THEN (SELECT COUNT(*) FROM runs prior WHERE prior.environment_id=runs.environment_id AND prior.status='queued' AND prior.created_at<=runs.created_at) ELSE 0 END FROM runs LEFT JOIN environments e ON e.id=runs.environment_id LEFT JOIN users u ON u.id=runs.requested_by LEFT JOIN component_releases cr ON cr.id=runs.component_release_id LEFT JOIN components c ON c.id=cr.component_id LEFT JOIN scenario_revisions sr ON sr.id=runs.scenario_revision_id LEFT JOIN scenarios sc ON sc.id=sr.scenario_id LEFT JOIN approvals a ON a.run_id=runs.id WHERE ` + where + ` ORDER BY runs.created_at DESC,runs.id DESC`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(append([]any{}, args...), limit, offset)
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunSummary{}
	for rows.Next() {
		var item RunSummary
		var version, created string
		var started, finished, archived sql.NullString
		if err = rows.Scan(&item.ArchiveStatus, &archived, &item.ArchiveSizeBytes, &item.ID, &item.Kind, &item.Status, &item.EnvironmentID, &item.EnvironmentName, &item.RequestedBy, &item.CreatedByName, &item.ComponentReleaseID, &item.ComponentID, &item.ComponentName, &version, &item.ScenarioID, &item.ScenarioName, &item.Action, &created, &started, &finished, &item.ApprovalID, &item.QueuePosition); err != nil {
			return nil, err
		}
		item.ArchivedAt = parseNullTime(archived)
		item.CreatedAt = parseTime(created)
		item.StartedAt = parseNullTime(started)
		item.FinishedAt = parseNullTime(finished)
		item.Name = item.ScenarioName
		if item.ComponentReleaseID != "" {
			item.Name = strings.TrimSpace(item.ComponentName+" "+version) + " test"
		}
		if item.Kind == domain.RunEnvironmentRollback {
			item.Name = item.EnvironmentName + " · 整集群回滚至干净状态"
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
