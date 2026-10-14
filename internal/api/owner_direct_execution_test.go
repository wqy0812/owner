package api

import (
	"codex/platform-demo/internal/seed"
	"net/http"
	"testing"
)

func TestRetiredOwnerApprovalRoutesAreUnavailable(t *testing.T) {
	f := newAPIFixture(t)
	for _, userID := range []string{seed.ComponentOwnerRuntimeID, seed.ScenarioOwnerID, seed.EnvironmentOwnerID, seed.PlatformAdminID} {
		cookie := f.session(userID)
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/approvals/batch-candidates"},
			{http.MethodPost, "/api/v1/approvals/batch"},
			{http.MethodPost, "/api/v1/approvals/unused/approve"},
			{http.MethodPost, "/api/v1/approvals/unused/reject"},
			{http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/review-submission"},
			{http.MethodGet, "/api/v1/component-releases/release-test-runtime-1.1.0/review-preview"},
			{http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/review-decision"},
		} {
			got := f.request(route.method, route.path, nil, cookie)
			if got.Code != http.StatusNotFound {
				t.Errorf("%s %s as %s: %d", route.method, route.path, userID, got.Code)
			}
		}
	}
	var count int
	if err := f.database.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='approvals'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("approval table count=%d err=%v", count, err)
	}
	if err := f.database.DB().QueryRow(`SELECT count(*) FROM pragma_table_info('component_releases') WHERE name LIKE 'review%'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("review columns count=%d err=%v", count, err)
	}
}
