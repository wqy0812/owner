package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
)

type synchronizedEventWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	status int
	flush  chan struct{}
}

func newSynchronizedEventWriter() *synchronizedEventWriter {
	return &synchronizedEventWriter{header: make(http.Header), flush: make(chan struct{}, 16)}
}

func (w *synchronizedEventWriter) Header() http.Header { return w.header }

func (w *synchronizedEventWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}

func (w *synchronizedEventWriter) Write(contents []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(contents)
}

func (w *synchronizedEventWriter) Flush() {
	select {
	case w.flush <- struct{}{}:
	default:
	}
}

func (w *synchronizedEventWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func waitForEventStream(t *testing.T, writer *synchronizedEventWriter, expected string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(writer.String(), expected) {
			return
		}
		select {
		case <-writer.flush:
		case <-deadline.C:
			t.Fatalf("event stream did not contain %q: %s", expected, writer.String())
		}
	}
}

func TestEventsStreamsVisibleHubUpdatesUntilClientDisconnects(t *testing.T) {
	f := newAPIFixture(t)
	user, err := f.platform.Identity().GetUser(context.Background(), seed.ComponentOwnerRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(context.WithValue(ctx, userContextKey, user))
	writer := newSynchronizedEventWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.(*Handler).events(writer, request)
	}()

	waitForEventStream(t, writer, "event: connected")
	f.platform.Hub().Publish("component.updated", map[string]any{"componentId": "component-test-runtime"})
	waitForEventStream(t, writer, "event: component.updated")
	if writer.header.Get("Content-Type") != "text/event-stream" || writer.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("event stream headers=%v", writer.header)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not stop after cancellation")
	}
}

func TestEventVisibilityEnforcesRunOwnershipAndWorkerErrorRole(t *testing.T) {
	f := newAPIFixture(t)
	handler := f.handler.(*Handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	user := func(id string) domain.User {
		value, err := f.platform.Identity().GetUser(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	alice := user(seed.ComponentOwnerRuntimeID)
	carol := user(seed.ScenarioOwnerID)
	dave := user(seed.EnvironmentOwnerID)
	runEvent := service.Event{Type: "run.log", Data: map[string]any{"runId": "run-installed-runtime-1.1"}}

	if !handler.eventVisible(request, alice, runEvent) || !handler.eventVisible(request, dave, runEvent) {
		t.Fatal("run owner or environment owner could not see a visible Run event")
	}
	if handler.eventVisible(request, carol, runEvent) {
		t.Fatal("unrelated scenario owner could see a component Run event")
	}
	if !handler.eventVisible(request, dave, service.Event{Type: "run.worker_error", Data: map[string]any{"error": "worker stopped"}}) {
		t.Fatal("environment owner could not see a worker error")
	}
	if handler.eventVisible(request, alice, service.Event{Type: "run.worker_error", Data: map[string]any{"error": "worker stopped"}}) {
		t.Fatal("component owner could see a global worker error")
	}
	if handler.eventVisible(request, alice, service.Event{Type: "approval.updated", Data: "invalid"}) {
		t.Fatal("malformed approval event was visible")
	}
	if !handler.eventVisible(request, carol, service.Event{Type: "scenario_revision.updated", Data: map[string]any{"scenarioRevisionId": "revision-1"}}) {
		t.Fatal("non-Run workflow event was hidden")
	}
}
