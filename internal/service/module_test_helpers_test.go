package service

import (
	"testing"

	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

func testDatabase(p *Platform) *store.Store { return p.catalog.store.(*store.Store) }

func newTestPlatform(t testing.TB, database *store.Store, runner any, hub *EventHub) *Platform {
	t.Helper()
	testutil.CompanionCLI(t)
	backend := testutil.AdaptRunner(runner)
	p, err := NewPlatform(database, RunnerDependencies{Workspaces: backend, Runtime: backend, Jobs: backend}, hub)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func setTestRunner(t testing.TB, p *Platform, runner any) {
	t.Helper()
	testutil.CompanionCLI(t)
	backend := testutil.AdaptRunner(runner)
	p.catalog.inspector = backend
	p.execution.inspector = backend
	p.execution.jobs = backend
	p.planner.runtime = backend
	p.releaseRules.inspector = backend
	p.rollback.inspector = backend
	p.executor.jobs = backend
	p.workspaceVerifier.inspector = backend
}
