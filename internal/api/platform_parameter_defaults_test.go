package api

import (
	"net/http"
	"testing"

	"codex/platform-demo/internal/seed"
)

func TestEnvironmentParameterDefaultAPIPermissionsAndPresence(t *testing.T) {
	f := newAPIFixture(t)
	admin, owner := f.session(seed.PlatformAdminID), f.session(seed.EnvironmentOwnerID)
	created := f.request(http.MethodPost, "/api/v1/environment-parameter-definitions", map[string]any{"label": "Enabled", "description": "Feature switch", "type": "boolean", "defaultValue": false}, admin)
	if created.Code != http.StatusCreated {
		t.Fatal(created.Body.String())
	}
	data := decodeEnvelope(t, created)["data"].(map[string]any)
	if data["defaultValue"] != false {
		t.Fatalf("false default lost: %#v", data)
	}
	route := "/api/v1/environment-parameter-definitions/" + data["id"].(string) + "/default"
	for _, test := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{}, http.StatusBadRequest},
		{map[string]any{"defaultValue": "false"}, http.StatusBadRequest},
		{map[string]any{"defaultValue": true}, http.StatusOK},
		{map[string]any{"defaultValue": nil}, http.StatusOK},
	} {
		got := f.request(http.MethodPut, route, test.body, admin)
		if got.Code != test.status {
			t.Fatalf("body=%#v status=%d response=%s", test.body, got.Code, got.Body.String())
		}
	}
	if got := f.request(http.MethodPut, route, map[string]any{"defaultValue": true}, owner); got.Code != http.StatusForbidden {
		t.Fatalf("owner default update status=%d", got.Code)
	}
}
