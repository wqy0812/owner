package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

// Exercise authentication, Store reads, Readiness, DTO assembly and JSON output
// using an isolated synthetic catalog. No server or operational database needed.
func BenchmarkComponentsHTTP(b *testing.B) {
	benchmarkComponentsHTTP(b, false)
}

func BenchmarkComponentsHTTPWithPlaybooks(b *testing.B) {
	benchmarkComponentsHTTP(b, true)
}

func benchmarkComponentsHTTP(b *testing.B, withPlaybooks bool) {
	for _, count := range []int{10, 40} {
		b.Run(fmt.Sprintf("components=%d/releases=3", count), func(b *testing.B) {
			ctx := context.Background()
			database, err := store.Open(ctx, ":memory:")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = database.Close() })
			now := time.Now().UTC()
			owner := domain.User{ID: "benchmark-owner", Name: "Owner", Role: domain.RoleComponentOwner, CreatedAt: now}
			if err := database.UpsertUser(ctx, owner); err != nil {
				b.Fatal(err)
			}
			for _, category := range []domain.PlatformOptionCategory{
				{ID: "hosts", Key: "hostGroup", Label: "Hosts", Kind: domain.PlatformOptionHostGroup, CreatedBy: owner.ID, CreatedAt: now},
				{ID: "architecture", Key: "architecture", Label: "Architecture", Kind: domain.PlatformOptionEnvironmentDimension, CreatedBy: owner.ID, CreatedAt: now},
			} {
				if err := database.UpsertPlatformOptionCategory(ctx, category); err != nil {
					b.Fatal(err)
				}
			}
			for _, option := range []domain.PlatformOption{
				{ID: "nodes", CategoryID: "hosts", Value: "nodes", Label: "Nodes", CreatedBy: owner.ID, CreatedAt: now},
				{ID: "arch", CategoryID: "architecture", Value: "amd64", Label: "Architecture", CreatedBy: owner.ID, CreatedAt: now},
			} {
				if err := database.UpsertPlatformOption(ctx, option); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("component-%03d", i)
				component := domain.Component{ID: id, Slug: id, Name: id, Layer: domain.LayerRuntimeState, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
				if err := database.CreateComponent(ctx, component); err != nil {
					b.Fatal(err)
				}
				for j := 0; j < 3; j++ {
					r := domain.ComponentRelease{ID: fmt.Sprintf("%s-r%d", id, j), ComponentID: id, Version: fmt.Sprintf("%d.0.0", j), Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, EnvironmentConstraints: map[string]any{"architecture": []string{"amd64"}}}
					for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionVerify, domain.ActionRollback} {
						r.Actions = append(r.Actions, domain.ActionDefinition{ID: r.ID + "-" + string(kind), ReleaseID: r.ID, Name: string(kind), Kind: kind, Playbook: "fixtures/" + string(kind) + ".yml", HostGroup: "nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow})
					}
					if err := database.CreateComponentRelease(ctx, r); err != nil {
						b.Fatal(err)
					}
				}
			}
			var runner service.ActionRunner
			if withPlaybooks {
				root := b.TempDir()
				if err := os.MkdirAll(filepath.Join(root, "fixtures"), 0o700); err != nil {
					b.Fatal(err)
				}
				for _, kind := range []string{"install", "verify", "rollback"} {
					if err := os.WriteFile(filepath.Join(root, "fixtures", kind+".yml"), []byte("- hosts: all\n  tasks: []\n"), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				for i := 0; i < 64; i++ {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("resource-%03d.txt", i)), []byte(strings.Repeat("x", 16*1024)), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				runner = &ansible.Runner{AllowedRoot: root}
			}
			platform := service.NewPlatform(database, runner, nil)
			b.Cleanup(platform.Close)
			handler := NewHandler(platform, nil)
			token := "benchmark-session"
			if err := database.CreateSession(ctx, fmt.Sprintf("%x", sha256.Sum256([]byte(token))), owner.ID, now.Add(time.Hour)); err != nil {
				b.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/components?view=contracts", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			check := httptest.NewRecorder()
			handler.ServeHTTP(check, request)
			var envelope struct {
				Items []domain.Component `json:"items"`
			}
			if err := json.Unmarshal(check.Body.Bytes(), &envelope); err != nil || check.Code != http.StatusOK || len(envelope.Items) != count {
				b.Fatalf("incomplete benchmark response: %s, err=%v", check.Body.String(), err)
			}
			for _, component := range envelope.Items {
				if len(component.Releases) != 3 {
					b.Fatalf("missing releases for %s", component.ID)
				}
				for _, release := range component.Releases {
					if release.Readiness.Status != domain.ReadinessBlocked || len(release.Readiness.Blockers) != 2 {
						b.Fatalf("benchmark skipped or broke Readiness: %+v", release.Readiness)
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					b.Fatalf("status=%d: %s", response.Code, response.Body.String())
				}
			}
		})
	}
}
