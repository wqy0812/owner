package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

type countedCatalogQuery struct {
	queryer
	count       int
	beforeQuery func(int)
}

func (q *countedCatalogQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.count++
	if q.beforeQuery != nil {
		q.beforeQuery(q.count)
	}
	return q.queryer.QueryContext(ctx, query, args...)
}

func TestComponentListBatchesPreserveCompleteContracts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-owner-a", Role: domain.RoleComponentOwner}
	for _, id := range []string{"upstream", "subject"} {
		if err := s.CreateComponent(ctx, componentFixture(id, owner.ID)); err != nil {
			t.Fatal(err)
		}
	}
	upstream := releaseFixture("upstream-r1", "upstream", "1.0.0", domain.ReleaseReleased)
	if err := s.CreateComponentRelease(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < releaseReadBatchSize+1; i++ {
		r := releaseFixture(fmt.Sprintf("subject-%03d", i), "subject", fmt.Sprintf("%d.0.0", i), domain.ReleaseDraft)
		r.CreatedAt = testNow.Add(time.Duration(i) * time.Second)
		r.Dependencies = []domain.ComponentDependency{{ID: r.ID + "-dep", ReleaseID: r.ID, UpstreamComponentID: upstream.ComponentID, UpstreamReleaseID: upstream.ID, Purpose: "runtime", ParameterMappings: []domain.ParameterMapping{}}}
		// Reverse insertion order detects child ordering changes in batch reads.
		for _, name := range []string{"z", "a"} {
			r.Artifacts = append(r.Artifacts, domain.ComponentArtifact{ID: r.ID + "-artifact-" + name, ReleaseID: r.ID, Alias: name, Filename: name + ".tgz", SHA256: "content-digest", SourceURL: "https://files.example.test/" + name, CreatedBy: owner.ID, SourceUpdatedBy: owner.ID, CreatedAt: testNow, SourceUpdatedAt: testNow})
			r.Images = append(r.Images, domain.ComponentImage{ID: r.ID + "-image-" + name, ReleaseID: r.ID, LogicalName: name, Digest: "image-digest", SourceRef: "registry.example.test/" + name, CreatedBy: owner.ID, SourceUpdatedBy: owner.ID, CreatedAt: testNow, SourceUpdatedAt: testNow})
		}
		if err := s.CreateClonedComponentRelease(ctx, r, domain.AuditEvent{ID: r.ID + "-audit", ActorID: owner.ID, Action: "component_release.created", ResourceType: "component_release", ResourceID: r.ID, CreatedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}

	// A list must not need another connection while holding its read cursor.
	s.DB().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := &countedCatalogQuery{queryer: tx}
	components, err := listComponents(ctx, q, owner)
	if err != nil {
		t.Fatal(err)
	}
	if q.count != 12 { // components + releases + two batches of five child tables
		t.Fatalf("queries=%d, want 12 for 258 releases", q.count)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(components) != 2 {
		t.Fatalf("components=%d", len(components))
	}
	for _, component := range components {
		listed, err := s.ListComponentReleases(ctx, component.ID, false)
		if err != nil || !reflect.DeepEqual(listed, component.Releases) {
			t.Fatalf("component list differs from release list for %s: %v", component.ID, err)
		}
		for i, release := range component.Releases {
			if i > 0 && release.CreatedAt.After(component.Releases[i-1].CreatedAt) {
				t.Fatal("release ordering changed")
			}
			single, err := s.GetComponentRelease(ctx, release.ID)
			if err != nil || !reflect.DeepEqual(single, release) {
				t.Fatalf("batch contract differs from individual read for %s: %v", release.ID, err)
			}
		}
	}
	released, err := s.ListComponentReleases(ctx, "subject", true)
	if err != nil || len(released) != 0 {
		t.Fatalf("drafts in released-only list: %v %v", released, err)
	}
	public, err := s.ListComponents(ctx, domain.User{ID: "scenario-owner-a", Role: domain.RoleScenarioOwner})
	if err != nil || len(public) != 1 || public[0].ID != upstream.ComponentID {
		t.Fatalf("non-owner visibility: %v %v", public, err)
	}
}

func TestComponentListUsesOneSnapshotDuringConcurrentEdit(t *testing.T) {
	reader, writer := catalogStorePair(t)
	ctx := context.Background()
	c := componentFixture("snapshot", "component-owner-a")
	if err := writer.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("snapshot-r1", c.ID, "1.0.0", domain.ReleaseDraft)
	r.Candidate = true
	if err := writer.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := &countedCatalogQuery{queryer: tx, beforeQuery: func(count int) {
		if count == 3 { // release header read; child tables have not yet loaded
			r.Actions[0].Playbook = "fixtures/changed.yml"
			if err := writer.UpdateDraftRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
	}}
	viewer := domain.User{ID: "scenario-owner-a", Role: domain.RoleScenarioOwner}
	items, err := listComponents(ctx, q, viewer)
	if err != nil || len(items) != 1 || len(items[0].Releases) != 1 {
		t.Fatalf("mixed snapshot changed candidate visibility: %v %v", items, err)
	}
	if items[0].Releases[0].Actions[0].Playbook != "fixtures/install.yml" || !items[0].Releases[0].IsApprovedCandidate() {
		t.Fatal("candidate did not retain the approved contract within its snapshot")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	items, err = reader.ListComponents(ctx, viewer)
	if err != nil || len(items) != 0 {
		t.Fatalf("next request retained an invalidated candidate: %v %v", items, err)
	}
}

// Synthetic catalog sizes make this benchmark reproducible without a running
// platform, external inventory, or a copy of an operational database.
func BenchmarkListComponents(b *testing.B) {
	for _, count := range []int{10, 40} {
		b.Run(fmt.Sprintf("components=%d/releases=3", count), func(b *testing.B) {
			s := newTestStore(b)
			ctx := context.Background()
			viewer := domain.User{ID: "component-owner-a", Role: domain.RoleComponentOwner}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("component-%03d", i)
				if err := s.CreateComponent(ctx, componentFixture(id, viewer.ID)); err != nil {
					b.Fatal(err)
				}
				for j := 0; j < 3; j++ {
					r := releaseFixture(fmt.Sprintf("%s-release-%d", id, j), id, fmt.Sprintf("%d.0.0", j), domain.ReleaseDraft)
					if err := s.CreateComponentRelease(ctx, r); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				items, err := s.ListComponents(ctx, viewer)
				if err != nil || len(items) != count {
					b.Fatalf("components=%d err=%v", len(items), err)
				}
			}
		})
	}
}
