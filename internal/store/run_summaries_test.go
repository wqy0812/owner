package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunSummaryPaginationVisibilityAndFilters(t *testing.T) {
	s := evidenceHistoryStore(t, 305)
	ctx := context.Background()
	// Equal timestamps exercise the stable ID tie-breaker across page boundaries.
	if _, err := s.DB().Exec(`UPDATE runs SET created_at=?,status=CASE WHEN id>='history-000250' THEN 'queued' ELSE status END`, timeText(testNow)); err != nil {
		t.Fatal(err)
	}
	for _, user := range []domain.User{{ID: "admin", Role: domain.RolePlatformAdmin}, {ID: "component-owner-a", Role: domain.RoleComponentOwner}, {ID: "environment-owner-a", Role: domain.RoleEnvironmentOwner}} {
		page, err := s.ListRunSummaries(ctx, user, RunListOptions{Page: 2, PageSize: 50, Filter: "all"})
		if err != nil || page.Total != 305 || len(page.Items) != 50 || page.Items[0].ID != "history-000254" || page.Items[49].ID != "history-000205" {
			t.Fatalf("%s page=%+v err=%v", user.Role, page, err)
		}
		for _, tc := range []struct {
			filter string
			total  int
		}{{"active", 55}, {"finished", 250}} {
			p, e := s.ListRunSummaries(ctx, user, RunListOptions{Page: 1, PageSize: 100, Filter: tc.filter, EnvironmentID: "history-environment"})
			if e != nil || p.Total != tc.total {
				t.Fatalf("filter %s %v %+v", tc.filter, e, p)
			}
		}
		p, e := s.ListRunSummaries(ctx, user, RunListOptions{Page: 99, PageSize: 50})
		if e != nil || p.Total != 305 || len(p.Items) != 0 {
			t.Fatalf("out of range: %+v %v", p, e)
		}
		p, e = s.ListRunSummaries(ctx, user, RunListOptions{Page: 1, PageSize: 4, EnvironmentID: "missing"})
		if e != nil || p.Total != 0 {
			t.Fatalf("environment filter: %+v %v", p, e)
		}
	}
	for _, user := range []domain.User{{ID: "scenario-owner-a", Role: domain.RoleScenarioOwner}, {ID: "component-owner-b", Role: domain.RoleComponentOwner}, {ID: "other", Role: domain.RoleEnvironmentOwner}} {
		p, e := s.ListRunSummaries(ctx, user, RunListOptions{Page: 1, PageSize: 50})
		if e != nil || p.Total != 0 {
			t.Fatalf("leaked %s: %+v %v", user.ID, p, e)
		}
	}
	// An arbitrary old Run still resolves independently of the list page.
	if run, err := s.GetRun(ctx, "history-000000"); err != nil || run.ID == "" {
		t.Fatalf("history detail: %v", err)
	}
}

type summaryQueryGuard struct {
	queryer
	calls int
}

func (q *summaryQueryGuard) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.calls++
	for _, heavy := range []string{"run_logs", "run_steps", "backup", "execution_snapshot_json"} {
		if strings.Contains(query, heavy) {
			panic("summary read accessed " + heavy)
		}
	}
	return q.queryer.QueryContext(ctx, query, args...)
}
func TestRunSummaryQueryIsBatchedAndLight(t *testing.T) {
	s := evidenceHistoryStore(t, 305)
	for _, size := range []int{1, 50, 100} {
		q := &summaryQueryGuard{queryer: s.DB()}
		items, err := readRunSummaries(context.Background(), q, "1=1", nil, size, 0)
		if err != nil || len(items) != size || q.calls != 1 {
			t.Fatalf("%d: rows=%d calls=%d err=%v", size, len(items), q.calls, err)
		}
		body, _ := json.Marshal(items)
		for _, field := range []string{"steps", "logTail", "backups", "inputSnapshot", "resolvedParameters"} {
			if strings.Contains(string(body), `"`+field+`"`) {
				t.Fatalf("heavy field %s", field)
			}
		}
	}
}
