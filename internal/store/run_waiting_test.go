package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"strings"
	"testing"
	"time"
)

func TestWaitingObservationsSurviveLogTailAndClearOnResult(t *testing.T) {
	s := evidenceHistoryStore(t, 0)
	ctx := context.Background()
	r := evidenceRun("waiting", "digest", "install_verify", "", "", time.Now())
	r.Status, r.FinishedAt = domain.RunRunning, nil
	if err := s.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	appendLog := func(stream, message string) {
		t.Helper()
		if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: stream, Message: message, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	appendLog("event", `{"kind":"waiting","stepId":"step-a","host":"node-1","task":"Ready","result":{"waiting":{"attempt":2,"observed":"Pending","deadline":"2026-09-06T00:01:00Z"}}}`)
	for i := 0; i < 260; i++ {
		appendLog("stdout", "other host log")
	}
	appendLog("event", "malformed event")
	got, err := s.ListRunWaitingObservations(ctx, r.ID)
	if err != nil || len(got) != 1 || !strings.Contains(string(got[0]), "Pending") {
		t.Fatalf("waiting lost: %s %v", got, err)
	}
	appendLog("event", `{"kind":"result","stepId":"step-a","host":"node-1","task":"Ready","status":"ok"}`)
	got, err = s.ListRunWaitingObservations(ctx, r.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("completed task still waiting: %s %v", got, err)
	}
}
