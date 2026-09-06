package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestRunLogsSurviveTransientWriterContention(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seed := evidenceHistoryStore(t, 0)
	run := evidenceRun("log-contention", "digest", "install_verify", "", "", testNow)
	if err := seed.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "logs.db")
	if _, err := seed.DB().ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Force immediate BUSY responses, as WAL contention can return before the
	// connection's busy timeout. The application must retain both event streams.
	s.DB().SetMaxOpenConns(1)
	if _, err := s.DB().ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE publication_state SET generation=generation WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, stream := range []string{"stdout", "event"} {
		go func(stream string) {
			_, err := s.AppendRunLog(ctx, domain.RunLog{RunID: run.ID, Stream: stream, Message: stream + " retained", CreatedAt: testNow})
			results <- err
		}(stream)
	}
	select {
	case err := <-results:
		t.Fatalf("log write ended before the transient lock cleared: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	logs, err := s.ListRunLogs(ctx, run.ID, 0, 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs lost or duplicated: %d, %v", len(logs), err)
	}
	seen := map[string]bool{}
	for _, log := range logs {
		if log.Message != log.Stream+" retained" || seen[log.Stream] {
			t.Fatalf("incorrect retained log: %+v", log)
		}
		seen[log.Stream] = true
	}
}
