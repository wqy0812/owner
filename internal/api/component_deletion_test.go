package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestEmptyComponentDeletionAPI(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.ComponentOwnerRuntimeID)
	created := f.request(http.MethodPost, "/api/v1/components", componentRequest("Empty Component", "empty-component"), owner)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %s", created.Body.String())
	}
	id := decodeEnvelope(t, created)["data"].(map[string]any)["id"].(string)
	url := "/api/v1/components/" + id
	detail := decodeEnvelope(t, f.request(http.MethodGet, url, nil, owner))["data"].(map[string]any)
	if detail["canDelete"] != true {
		t.Fatalf("empty component cannot be deleted: %#v", detail)
	}
	admin := f.session(seed.PlatformAdminID)
	if detail := decodeEnvelope(t, f.request(http.MethodGet, url, nil, admin))["data"].(map[string]any); detail["canDelete"] != false {
		t.Fatalf("admin eligibility=%v", detail["canDelete"])
	}
	for _, user := range []string{seed.ComponentOwnerK8sID, seed.EnvironmentOwnerID, seed.ScenarioOwnerID, seed.PlatformAdminID} {
		response := f.request(http.MethodDelete, url, nil, f.session(user))
		if response.Code != http.StatusForbidden {
			t.Fatalf("delete as %s: %d %s", user, response.Code, response.Body.String())
		}
	}
	events, unsubscribe := f.platform.Hub().Subscribe()
	defer unsubscribe()
	deleted := f.request(http.MethodDelete, url, nil, owner)
	if deleted.Code != http.StatusOK || decodeEnvelope(t, deleted)["data"].(map[string]any)["deleted"] != true {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	select {
	case event := <-events:
		if event.Type != "component.deleted" || event.Data.(map[string]any)["componentId"] != id {
			t.Fatalf("event=%+v", event)
		}
		if _, err := f.database.GetComponent(ctx, id, false); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("event before deletion committed: %v", err)
		}
	default:
		t.Fatal("missing deletion event")
	}
	if got := f.request(http.MethodDelete, url, nil, owner); got.Code != http.StatusNotFound {
		t.Fatalf("repeat delete: %d %s", got.Code, got.Body.String())
	}
	var audits int
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE resource_id=? AND action IN ('component.created','component.deleted')`, id).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit history=%d %v", audits, err)
	}
}

func TestEmptyComponentDeletionAPIRejectsAllVersionStates(t *testing.T) {
	for _, state := range []domain.ReleaseStatus{domain.ReleaseDraft, domain.ReleaseReleased, domain.ReleaseDeprecated} {
		t.Run(string(state), func(t *testing.T) {
			f := newAPIFixture(t)
			owner := f.session(seed.ComponentOwnerRuntimeID)
			created := f.request(http.MethodPost, "/api/v1/components", componentRequest("Nonempty", "nonempty"), owner)
			id := decodeEnvelope(t, created)["data"].(map[string]any)["id"].(string)
			r := domain.ComponentRelease{ID: "nonempty-r1", ComponentID: id, Version: "1.0.0", Status: state, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: time.Now().UTC(), EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}}
			if state == domain.ReleaseReleased {
				r.ReleasedAt = &r.CreatedAt
			}
			if err := f.database.CreateComponentRelease(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			url := "/api/v1/components/" + id
			if detail := decodeEnvelope(t, f.request(http.MethodGet, url, nil, owner))["data"].(map[string]any); detail["canDelete"] != false {
				t.Fatalf("nonempty eligibility=%v", detail["canDelete"])
			}
			response := f.request(http.MethodDelete, url, nil, owner)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "component.not_empty") || !strings.Contains(response.Body.String(), "仅没有任何版本") {
				t.Fatalf("delete: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestEmptyComponentDeletionAPIReferenceAndAuditFailure(t *testing.T) {
	for _, blocked := range []string{"reference", "audit"} {
		t.Run(blocked, func(t *testing.T) {
			f := newAPIFixture(t)
			ctx := context.Background()
			owner := f.session(seed.ComponentOwnerRuntimeID)
			created := f.request(http.MethodPost, "/api/v1/components", componentRequest("Retained", "retained"), owner)
			id := decodeEnvelope(t, created)["data"].(map[string]any)["id"].(string)
			if blocked == "reference" {
				_, err := f.database.DB().ExecContext(ctx, `INSERT INTO component_dependencies(kind,id,release_id,upstream_component_id,upstream_release_id) SELECT 'execution','empty-ref',id,?,id FROM component_releases LIMIT 1`, id)
				if err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.database.DB().ExecContext(ctx, `CREATE TRIGGER reject_empty_delete_audit BEFORE INSERT ON audit_events WHEN NEW.action='component.deleted' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			url := "/api/v1/components/" + id
			if detail := decodeEnvelope(t, f.request(http.MethodGet, url, nil, owner))["data"].(map[string]any); detail["canDelete"] != (blocked == "audit") {
				t.Fatalf("eligibility=%v", detail["canDelete"])
			}
			events, unsubscribe := f.platform.Hub().Subscribe()
			defer unsubscribe()
			response := f.request(http.MethodDelete, url, nil, owner)
			if blocked == "reference" && (response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "component.still_referenced")) {
				t.Fatalf("reference: %d %s", response.Code, response.Body.String())
			}
			if response.Code < 400 {
				t.Fatalf("deletion unexpectedly succeeded: %s", response.Body.String())
			}
			if _, err := f.database.GetComponent(ctx, id, false); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-events:
				t.Fatalf("failed deletion published event: %+v", event)
			default:
			}
		})
	}
}
