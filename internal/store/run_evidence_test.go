package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func evidenceRun(id, digest, evidence, runtime, version string, at time.Time) domain.Run {
	return domain.Run{ID: id, Kind: domain.RunComponentTest, Status: domain.RunSucceeded, RequestedBy: "component-owner-a", EnvironmentID: "history-environment", EnvironmentRevisionID: "history-environment-r1", ComponentReleaseID: "history-release", Action: domain.ActionInstall, InputSnapshot: map[string]any{"componentReleaseSpecDigest": digest, "componentTestEvidence": evidence, "runtimeCompatibility": map[string]any{"runtime": runtime, "version": version}}, CreatedAt: at, FinishedAt: &at}
}

func TestIndexedEvidenceMatchesSnapshotQueries(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	cases := []struct {
		id, digest, evidence, runtime string
		status                        domain.RunStatus
		noFinish                      bool
	}{
		{"install", "current", "install_verify", "runtime-a", domain.RunSucceeded, false},
		{"rollback", "current", "rollback_verify", "runtime-a", domain.RunSucceeded, false},
		{"self-verify", "current", "rollback_self_verify", "runtime-a", domain.RunSucceeded, true},
		{"evolution", "current", "evolution_round_trip", "runtime-a", domain.RunSucceeded, false},
		{"other-runtime", "current", "install_verify", "runtime-b", domain.RunSucceeded, false},
		{"other-digest", "old", "install_verify", "runtime-a", domain.RunSucceeded, false},
		{"failed-install", "current", "install_verify", "runtime-a", domain.RunFailed, false},
		{"rollback-only", "current", "rollback_only", "runtime-a", domain.RunSucceeded, false},
	}
	for i, item := range cases {
		run := evidenceRun(item.id, item.digest, item.evidence, item.runtime, "version-a", testNow.Add(time.Duration(i)*time.Minute))
		run.Status = item.status
		if item.noFinish {
			run.FinishedAt = nil
		}
		if err := s.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, runtime := range []string{""} {
		for _, digest := range []string{"current", "old", "absent"} {
			for _, evidence := range []string{"install_verify", "rollback_verify", "evolution_round_trip"} {
				var expected string
				err := s.DB().QueryRowContext(ctx, `SELECT id FROM runs WHERE kind='component_test' AND status='succeeded' AND component_release_id=?
AND json_extract(input_snapshot_json,'$.componentReleaseSpecDigest')=?
AND (?='' OR (json_extract(input_snapshot_json,'$.runtimeCompatibility.runtime')=? AND json_extract(input_snapshot_json,'$.runtimeCompatibility.version')=?))
AND CASE WHEN ?='rollback_verify' THEN json_extract(input_snapshot_json,'$.componentTestEvidence') IN ('rollback_verify','rollback_self_verify') ELSE json_extract(input_snapshot_json,'$.componentTestEvidence')=? END
ORDER BY COALESCE(finished_at,created_at) DESC,created_at DESC,id DESC LIMIT 1`, "history-release", digest, runtime, runtime, "version-a", evidence, evidence).Scan(&expected)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					t.Fatal(err)
				}
				actual, err := s.lookupComponentEvidence(ctx, "history-release", digest, evidence)
				if err != nil || actual != expected {
					t.Fatalf("%s/%s/%s: got %q, want %q, err=%v", runtime, digest, evidence, actual, expected, err)
				}
			}
		}
	}
}

func TestEvidenceProjectionsTrackSourceAndCannotBeWritten(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	run := evidenceRun("projection", "current", "install_verify", "runtime-a", "version-a", testNow)
	run.Status, run.FinishedAt = domain.RunRunning, nil
	if err := s.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	if id, err := s.lookupComponentEvidence(ctx, run.ComponentReleaseID, "current", "install_verify"); err != nil || id != "" {
		t.Fatalf("running evidence=%q %v", id, err)
	}
	if err := s.UpdateRunDeliveryResults(ctx, run.ID, []string{"delivered"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunRunning}, domain.RunSucceeded, "", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, err := s.lookupComponentEvidence(ctx, run.ComponentReleaseID, "current", "install_verify"); err != nil || id != run.ID {
		t.Fatalf("completed evidence=%q %v", id, err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE runs SET component_spec_digest='forged' WHERE id=?`, run.ID); err == nil {
		t.Fatal("generated digest accepted an independent write")
	}
	// Only modify synthetic records: prove malformed metadata cannot be coerced
	// into a valid string key, and absence remains NULL rather than an empty key.
	for _, snapshot := range []string{`{}`, `{"componentReleaseSpecDigest":123,"componentTestEvidence":true,"runtimeCompatibility":{"runtime":[],"version":{}}}`} {
		if _, err := s.DB().ExecContext(ctx, `UPDATE runs SET input_snapshot_json=? WHERE id=?`, snapshot, run.ID); err != nil {
			t.Fatal(err)
		}
		var digest, evidence sql.NullString
		if err := s.DB().QueryRowContext(ctx, `SELECT component_spec_digest,component_evidence_kind FROM runs WHERE id=?`, run.ID).Scan(&digest, &evidence); err != nil {
			t.Fatal(err)
		}
		if digest.Valid || evidence.Valid {
			t.Fatal("malformed or missing metadata was converted to a string")
		}
	}
	if id, err := s.lookupComponentEvidence(ctx, run.ComponentReleaseID, "current", "install_verify"); err != nil || id != "" {
		t.Fatalf("stale indexed evidence=%q %v", id, err)
	}
}

func TestRunEvidenceQueriesUseSearchIndexes(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	check := func(query string, args []any, index string, forbidSort bool) {
		t.Helper()
		rows, err := s.DB().QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), index) || strings.Contains(plan.String(), "SCAN runs") || (forbidSort && strings.Contains(plan.String(), "TEMP B-TREE")) {
			t.Fatalf("unexpected plan:\n%s", plan.String())
		}
	}
	for range []int{0} {
		index := "idx_runs_component_evidence"
		for _, evidence := range []string{"install_verify", "rollback_verify", "evolution_round_trip"} {
			query, args := componentEvidenceQuery("history-release", "current", evidence)
			check(query, args, index, evidence != "rollback_verify")
		}
	}
	check(`SELECT id FROM run_steps WHERE run_id=? ORDER BY rowid`, []any{"run"}, "idx_run_steps_run", true)
	check(`SELECT id FROM action_definitions WHERE release_id=? ORDER BY kind,name`, []any{"release"}, "idx_actions_release", true)
	check(`SELECT COUNT(*) FROM runs WHERE kind='component_test' AND component_release_id=? AND status IN ('awaiting_approval','queued','running')`, []any{"release"}, "idx_runs_active_component", true)
}

func evidenceHistoryStore(t testing.TB, count int) *Store {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	c := componentFixture("history-component", "component-owner-a")
	if err := s.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("history-release", c.ID, "1.0.0", domain.ReleaseDraft)); err != nil {
		t.Fatal(err)
	}
	e := domain.Environment{ID: "history-environment", Name: "History", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	er := domain.EnvironmentRevision{ID: "history-environment-r1", EnvironmentID: e.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, e, er); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,action_kind,input_snapshot_json,created_at,finished_at) VALUES(?,'component_test','succeeded','component-owner-a','history-environment','history-environment-r1','history-release',?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	for i := 0; i < count; i++ {
		digest, evidence, action := "older-contract", "install_verify", "install"
		if i%2 != 0 {
			evidence, action = "rollback_verify", "rollback"
		}
		if i >= count-2 {
			digest = "current-contract"
		}
		snapshot := map[string]any{"componentReleaseSpecDigest": digest, "componentTestEvidence": evidence, "runtimeCompatibility": map[string]any{"runtime": "runtime-a", "version": "version-a"}, "steps": []any{map[string]any{"releaseId": "history-release"}}}
		at := timeText(testNow.Add(time.Duration(i) * time.Second))
		if _, err := insert.ExecContext(ctx, fmt.Sprintf("history-%06d", i), action, jsonText(snapshot), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s
}

func BenchmarkComponentEvidenceHistory(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("runs=%d", count), func(b *testing.B) {
			s := evidenceHistoryStore(b, count)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				install, rollback, err := s.SuccessfulComponentEvidenceRunIDs(ctx, "history-release", "current-contract")
				if err != nil || install != fmt.Sprintf("history-%06d", count-2) || rollback != fmt.Sprintf("history-%06d", count-1) {
					b.Fatalf("evidence=%s/%s err=%v", install, rollback, err)
				}
			}
		})
	}
}
