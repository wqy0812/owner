package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil/perf"
)

func activityFixture(t testing.TB) (*Store, domain.Run) {
	s := evidenceHistoryStore(t, 0)
	r := evidenceRun("activity", "digest", "install_verify", "", "", testNow)
	r.Status = domain.RunRunning
	r.FinishedAt = nil
	if err := s.CreateRun(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	return s, r
}

func TestWaitingProjectionAtomicityKeysAndLifecycle(t *testing.T) {
	s, r := activityFixture(t)
	ctx := context.Background()
	appendEvent := func(message string) {
		t.Helper()
		if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "event", Message: message, CreatedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	assertWaits := func(count int, contains string) {
		t.Helper()
		items, err := s.ListRunWaitingObservations(ctx, r.ID)
		if err != nil || len(items) != count || !strings.Contains(fmt.Sprintf("%s", items), contains) {
			t.Fatalf("waits=%s err=%v", items, err)
		}
	}
	wait := func(host, observed string) string {
		return fmt.Sprintf(`{"kind":"waiting","stepId":"s","host":%q,"task":"Ready","result":{"waiting":{"observed":%q}}}`, host, observed)
	}
	appendEvent(wait("a", "first"))
	appendEvent(wait("a", "newest"))
	appendEvent(wait("b", "other"))
	assertWaits(2, "newest")
	for _, bad := range []string{"{", `{"kind":"waiting","stepId":4}`, `{"kind":"waiting","stepId":"s","host":"a","task":"Ready","result":{"waiting":{"attempt":"bad"}}}`} {
		appendEvent(bad)
	}
	assertWaits(2, "newest")
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO run_logs(run_id,stream,message,created_at) VALUES(?,'event',?,?)`, r.ID, wait("a", "rolled-back"), timeText(testNow)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertWaits(2, "newest")
	appendEvent(`{"kind":"result","stepId":"s","host":"a","task":"Ready","status":"ok"}`)
	assertWaits(1, "other")
	if _, err = s.DB().ExecContext(ctx, `DELETE FROM run_logs WHERE run_id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	assertWaits(0, "")
	appendEvent(wait("a", "pending"))
	assertWaits(1, "pending")
	if err = s.UpdateRunStatus(ctx, r.ID, []domain.RunStatus{domain.RunRunning}, domain.RunSucceeded, "", testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	appendEvent(wait("a", "late"))
	assertWaits(0, "")
}

func TestRunActivityTailCursorAndProjectionErrors(t *testing.T) {
	s, r := activityFixture(t)
	ctx := context.Background()
	for i := 0; i < 251; i++ {
		if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: fmt.Sprint(i), CreatedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	tail, err := s.ReadRunActivity(ctx, r.ID, nil, 500)
	if err != nil || len(tail.Logs) != 200 || tail.Logs[0].Message != "51" || tail.HasMore {
		t.Fatalf("tail=%+v %v", tail, err)
	}
	cursor := int64(0)
	seen := 0
	for {
		page, e := s.ReadRunActivity(ctx, r.ID, &cursor, 31)
		if e != nil {
			t.Fatal(e)
		}
		for _, log := range page.Logs {
			if log.ID <= cursor {
				t.Fatal("duplicate or unordered log")
			}
			cursor = log.ID
			seen++
		}
		if cursor != page.NextAfterID {
			t.Fatal("wrong cursor")
		}
		if !page.HasMore {
			break
		}
	}
	if seen != 251 || cursor != tail.NextAfterID {
		t.Fatalf("seen=%d cursor=%d", seen, cursor)
	}
	empty, err := s.ReadRunActivity(ctx, r.ID, &cursor, 31)
	if err != nil || empty.NextAfterID != cursor || len(empty.Logs) != 0 {
		t.Fatal(empty, err)
	}
	if _, err = s.DB().ExecContext(ctx, `DROP TABLE run_waiting_observations`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadRunActivity(ctx, r.ID, &cursor, 31); err == nil {
		t.Fatal("projection read failure was hidden")
	}
}

func TestWaitingReadUsesOnlyProjection(t *testing.T) {
	s, r := activityFixture(t)
	rows, err := s.DB().Query(`EXPLAIN QUERY PLAN SELECT message FROM run_waiting_observations WHERE run_id=? ORDER BY step_id,host,task`, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "SEARCH run_waiting_observations") || strings.Contains(plan.String(), "run_logs") || strings.Contains(plan.String(), "TEMP B-TREE") {
		t.Fatal(plan.String())
	}
}

func BenchmarkWaitingProjection(b *testing.B) {
	for _, count := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, r := activityFixture(b)
			ctx := context.Background()
			tx, err := s.DB().BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			stmt, err := tx.PrepareContext(ctx, `INSERT INTO run_logs(run_id,stream,message,created_at) VALUES(?,'event',?,?)`)
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < count; i++ {
				if _, err = stmt.ExecContext(ctx, r.ID, fmt.Sprintf(`{"kind":"waiting","stepId":"s","host":"h","task":"ready","result":{"waiting":{"attempt":%d}}}`, i), timeText(testNow)); err != nil {
					b.Fatal(err)
				}
			}
			stmt.Close()
			if err = tx.Commit(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				items, err := s.ListRunWaitingObservations(ctx, r.ID)
				if err != nil || len(items) != 1 {
					b.Fatal(items, err)
				}
			}
		})
	}
}

func TestWaitingPerformanceSamples(t *testing.T) {
	path := os.Getenv("CLUSTERFORGE_WAITING_PERFORMANCE_REPORT")
	if path == "" {
		t.Skip("opt-in isolated performance measurement")
	}
	var samples []perf.PerformanceSample
	for _, count := range []int{1000, 10000, 50000} {
		s, r := activityFixture(t)
		ctx := context.Background()
		tx, err := s.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO run_logs(run_id,stream,message,created_at) VALUES(?,'event',?,?)`)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < count; i++ {
			if _, err = stmt.ExecContext(ctx, r.ID, fmt.Sprintf(`{"kind":"waiting","stepId":"s","host":"h","task":"ready","result":{"waiting":{"attempt":%d}}}`, i), timeText(testNow)); err != nil {
				t.Fatal(err)
			}
		}
		stmt.Close()
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		for _, legacy := range []bool{true, false} {
			name := "projection"
			if legacy {
				name = "legacy"
			}
			samples = append(samples, perf.MeasureRequests(t, fmt.Sprintf("%d/%s", count, name), 31, func() error {
				if !legacy {
					_, err := s.ListRunWaitingObservations(ctx, r.ID)
					return err
				}
				rows, err := s.DB().QueryContext(ctx, `WITH events AS (
SELECT message,ROW_NUMBER() OVER(PARTITION BY json_extract(message,'$.stepId'),json_extract(message,'$.host'),json_extract(message,'$.task') ORDER BY id DESC) AS position
FROM run_logs WHERE run_id=? AND stream='event' AND json_valid(message) AND json_extract(message,'$.kind') IN ('waiting','result'))
SELECT message FROM events WHERE position=1 AND json_extract(message,'$.kind')='waiting'`, r.ID)
				if err != nil {
					return err
				}
				defer rows.Close()
				items := []json.RawMessage{}
				for rows.Next() {
					var message string
					if err = rows.Scan(&message); err != nil {
						return err
					}
					items = append(items, json.RawMessage(message))
				}
				if len(items) != 1 {
					return fmt.Errorf("wrong waiting result: %d", len(items))
				}
				return rows.Err()
			}))
		}
	}
	body, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunActivitySurvivesReopenAndReadsOneSnapshot(t *testing.T) {
	s, r := activityFixture(t)
	ctx := context.Background()
	message := `{"kind":"waiting","stepId":"s","host":"h","task":"ready","result":{"waiting":{"attempt":0}}}`
	if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "event", Message: message, CreatedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	if _, err := s.DB().ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 150; i++ {
			_, err := reopened.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "event", Message: fmt.Sprintf(`{"kind":"waiting","stepId":"s","host":"h","task":"ready","result":{"waiting":{"attempt":%d}}}`, i), CreatedAt: testNow})
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var readErr error
	for i := 0; i < 150; i++ {
		page, err := reopened.ReadRunActivity(ctx, r.ID, nil, 500)
		if err != nil {
			readErr = err
			break
		}
		if len(page.WaitingObservations) != 1 || page.Logs[len(page.Logs)-1].Message != string(page.WaitingObservations[0]) {
			readErr = fmt.Errorf("mixed activity snapshots: %+v", page)
			break
		}
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if _, err = reopened.DB().ExecContext(ctx, `DELETE FROM runs WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err = reopened.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM run_waiting_observations WHERE run_id=?`, r.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal(remaining, err)
	}
}
