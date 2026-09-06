package api

import (
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
	"testing"
)

func newAPITestPlatform(t testing.TB, database *store.Store, runner any, hub *service.EventHub) *service.Platform {
	t.Helper()
	testutil.CompanionCLI(t)
	backend := testutil.AdaptRunner(runner)
	p, err := service.NewPlatform(database, service.RunnerDependencies{Workspaces: backend, Runtime: backend, Jobs: backend}, hub)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
