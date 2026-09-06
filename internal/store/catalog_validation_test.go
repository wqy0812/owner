package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

func catalogStorePair(t *testing.T) (*Store, *Store) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, u := range []domain.User{
		{ID: "component-owner-a", Name: "Component Owner", Role: domain.RoleComponentOwner, CreatedAt: testNow},
		{ID: "scenario-owner-a", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: testNow},
		{ID: "environment-owner-a", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: testNow},
	} {
		if err := a.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	seedStoreCatalog(t, a)
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return a, b
}

func catalogAudit(id, action string) domain.AuditEvent {
	return domain.AuditEvent{ID: id, ActorID: "component-owner-a", Action: action, ResourceType: "catalog", ResourceID: id, CreatedAt: testNow}
}

// Both contenders use independent connection pools. Channels establish the
// stale-read boundary and control either serial order without timing sleeps.
func TestReleaseReviewAndCandidateCAS(t *testing.T) {
	for _, operation := range []string{"candidate", "review"} {
		for _, first := range []string{"edit", "transition"} {
			t.Run(operation+"/"+first, func(t *testing.T) {
				a, b := catalogStorePair(t)
				ctx := context.Background()
				c := componentFixture("cas-component", "component-owner-a")
				if err := a.CreateComponent(ctx, c); err != nil {
					t.Fatal(err)
				}
				r := releaseFixture("cas-release", c.ID, "1.0.0", domain.ReleaseDraft)
				if err := a.CreateComponentRelease(ctx, r); err != nil {
					t.Fatal(err)
				}
				captured, err := a.GetComponentRelease(ctx, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				transition := func() error {
					if operation == "candidate" {
						return a.SetReleaseCandidate(ctx, r.ID, true, captured.PublicationGeneration, domain.ComponentReleaseSpecDigest(captured))
					}
					return a.SubmitComponentReleaseReview(ctx, r.ID, domain.ComponentReleaseSpecDigest(captured), captured.PublicationGeneration, testNow)
				}
				edit := func() error {
					changed := captured
					changed.ReleaseNotes = "updated contract"
					return b.UpdateDraftRelease(ctx, changed)
				}
				ready, proceed, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				go func() { close(ready); <-proceed; done <- transition() }()
				<-ready
				if first == "edit" {
					if err := edit(); err != nil {
						t.Fatal(err)
					}
				}
				close(proceed)
				err = <-done
				if first == "edit" {
					if !errors.Is(err, domain.ErrConflict) {
						t.Fatalf("stale transition error=%v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if err := edit(); err != nil {
						t.Fatal(err)
					}
				}
				current, err := a.GetComponentRelease(ctx, r.ID)
				if err != nil || current.Candidate || current.Review.Status != domain.ReleaseReviewNotSubmitted {
					t.Fatalf("stale approval persisted: %+v error=%v", current, err)
				}
			})
		}
	}
}

func TestCatalogReferenceDeletionAndRetirementRace(t *testing.T) {
	for _, resource := range []string{"release", "category", "environment", "scenario", "variable"} {
		mutations := []string{"delete"}
		if resource != "variable" {
			mutations = append(mutations, "retire")
		}
		for _, mutation := range mutations {
			for _, order := range []string{"directory_first", "reference_first", "concurrent"} {
				t.Run(resource+"/"+mutation+"/"+order, func(t *testing.T) {
					a, b := catalogStorePair(t)
					ctx := context.Background()
					option := domain.PlatformOption{ID: "new-option", CategoryID: "category-architecture", Value: "arm64", Label: "arm64", CreatedBy: "component-owner-a", CreatedAt: testNow}
					if resource == "scenario" {
						option.CategoryID, option.Value = "category-hosts", "batch"
					}
					if err := a.UpsertPlatformOption(ctx, option); err != nil {
						t.Fatal(err)
					}
					if resource == "variable" {
						if err := a.UpsertEnvironmentVariableDefinition(ctx, domain.EnvironmentVariableDefinition{ID: "variable-region", Name: "REGION", Label: "Region", CreatedBy: "component-owner-a", CreatedAt: testNow}); err != nil {
							t.Fatal(err)
						}
					}
					component := componentFixture("ref-component", "component-owner-a")
					if err := a.CreateComponent(ctx, component); err != nil {
						t.Fatal(err)
					}
					release := releaseFixture("ref-release", component.ID, "1.0.0", domain.ReleaseDraft)
					release.EnvironmentConstraints = map[string]any{}
					if resource != "release" && resource != "category" {
						if err := a.CreateComponentRelease(ctx, release); err != nil {
							t.Fatal(err)
						}
						release, _ = a.GetComponentRelease(ctx, release.ID)
					}
					if resource == "release" || resource == "category" {
						release.EnvironmentConstraints = map[string]any{"architecture": []string{option.Value}}
					}
					// This snapshot represents successful service validation before either writer starts.
					snapshot, err := a.ReadCatalogValidation(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if resource == "release" || resource == "category" {
						if err := snapshot.Options.ValidateConstraints(release.EnvironmentConstraints); err != nil {
							t.Fatal(err)
						}
					}
					write := func() error {
						switch resource {
						case "release", "category":
							return a.CreateComponentRelease(ctx, release)
						case "environment", "variable":
							e := domain.Environment{ID: "ref-environment", Name: "Environment", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
							r := domain.EnvironmentRevision{ID: "ref-environment-r1", EnvironmentID: e.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), Variables: map[string]string{}, CreatedAt: testNow}
							if resource == "environment" {
								r.Facts["architecture"] = option.Value
							} else {
								r.Variables["REGION"] = "north"
							}
							return a.CreateEnvironment(ctx, e, r)
						default:
							sc := domain.Scenario{ID: "ref-scenario", Slug: "ref-scenario", Name: "Scenario", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
							return a.CreateScenario(ctx, sc, domain.ScenarioRevision{ID: "ref-scenario-r1", ScenarioID: sc.ID, Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: release.ID, Action: domain.ActionInstall, HostGroup: option.Value}}}, CreatedAt: testNow})
						}
					}
					mutate := func() error {
						audit := catalogAudit("directory-event", mutation)
						switch resource {
						case "category":
							if mutation == "retire" {
								return b.SetPlatformOptionCategoryRetired(ctx, option.CategoryID, &testNow, audit)
							}
							return b.DeletePlatformOptionCategory(ctx, option.CategoryID, audit)
						case "variable":
							return b.DeleteEnvironmentVariableDefinition(ctx, "variable-region", audit)
						default:
							if mutation == "retire" {
								return b.SetPlatformOptionRetired(ctx, option.ID, &testNow, audit)
							}
							return b.DeletePlatformOption(ctx, option.ID, audit)
						}
					}
					startWrite, startMutation := make(chan struct{}), make(chan struct{})
					written, mutated := make(chan error, 1), make(chan error, 1)
					go func() { <-startWrite; written <- write() }()
					go func() { <-startMutation; mutated <- mutate() }()
					var we, me error
					switch order {
					case "directory_first":
						close(startMutation)
						me = <-mutated
						close(startWrite)
						we = <-written
					case "reference_first":
						close(startWrite)
						we = <-written
						close(startMutation)
						me = <-mutated
					default:
						close(startWrite)
						close(startMutation)
						we, me = <-written, <-mutated
					}
					for _, err := range []error{we, me} {
						if err != nil && !errors.Is(err, domain.ErrConflict) {
							t.Fatalf("unexpected error=%v", err)
						}
					}
					if mutation == "delete" && we == nil && me == nil {
						t.Fatal("deletion and new reference both committed")
					}
					if order == "directory_first" && (me != nil || !errors.Is(we, domain.ErrConflict)) {
						t.Fatalf("stale reference write=%v mutation=%v", we, me)
					}
					if order == "reference_first" && we != nil {
						t.Fatal(we)
					}
					// A second attempt after the directory mutation must never introduce a reference.
					if me == nil && we != nil {
						if err := write(); !errors.Is(err, domain.ErrConflict) {
							t.Fatalf("retry after catalog mutation=%v", err)
						}
					}
				})
			}
		}
	}
}

func TestRetiredReferencesPreservedButCopiesRejected(t *testing.T) {
	a, b := catalogStorePair(t)
	ctx := context.Background()
	c := componentFixture("retained", "component-owner-a")
	if err := a.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("retained-r1", c.ID, "1.0.0", domain.ReleaseDraft)
	if err := a.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPlatformOptionRetired(ctx, "option-amd64", &testNow, catalogAudit("retire-architecture", "retire")); err != nil {
		t.Fatal(err)
	}
	r.ReleaseNotes = "unrelated edit"
	if err := a.UpdateDraftRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	copy := r
	copy.ID, copy.LineID, copy.LineName, copy.Version = "copy", "line-copy", "copy", "2.0.0"
	for i := range copy.Actions {
		copy.Actions[i].ID = fmt.Sprintf("copy-%d", i)
	}
	if err := a.CreateClonedComponentRelease(ctx, copy, catalogAudit("copy-audit", "clone")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("retired clone=%v", err)
	}
	if _, err := a.GetComponentRelease(ctx, copy.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("partial clone=%v", err)
	}
	r.EnvironmentConstraints = map[string]any{}
	if err := a.UpdateDraftRelease(ctx, r); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("fixed branch must retain retired scope: %v", err)
	}
}

func TestEnvironmentRevisionGuardAndRetainedRestore(t *testing.T) {
	a, b := catalogStorePair(t)
	ctx := context.Background()
	e := domain.Environment{ID: "versioned", Name: "Environment", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	r1 := domain.EnvironmentRevision{ID: "versioned-r1", EnvironmentID: e.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
	if err := a.CreateEnvironment(ctx, e, r1); err != nil {
		t.Fatal(err)
	}
	r2 := r1
	r2.ID, r2.Revision = "versioned-r2", 2
	r2.Facts = map[string]any{}
	if err := b.CreateEnvironmentRevision(ctx, r2, EnvironmentRevisionWrite{ExpectedCurrentRevisionID: r1.ID}); err != nil {
		t.Fatal(err)
	}
	stale := r1
	stale.ID, stale.Revision = "stale", 3
	if err := a.CreateEnvironmentRevision(ctx, stale, EnvironmentRevisionWrite{ExpectedCurrentRevisionID: r1.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale write=%v", err)
	}
	if err := b.SetPlatformOptionRetired(ctx, "option-amd64", &testNow, catalogAudit("retire-env", "retire")); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateEnvironmentRevision(ctx, stale, EnvironmentRevisionWrite{ExpectedCurrentRevisionID: r2.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reintroduce retired value=%v", err)
	}
	if err := a.CreateEnvironmentRevision(ctx, stale, EnvironmentRevisionWrite{ExpectedCurrentRevisionID: r2.ID, RestoreSourceRevisionID: r1.ID}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM environment_revisions WHERE environment_id=?", e.ID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("revision count=%d err=%v", count, err)
	}
}

func TestCatalogFenceDoesNotAdvancePublicationEpoch(t *testing.T) {
	a, _ := catalogStorePair(t)
	ctx := context.Background()
	before, err := a.GetPublicationEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.beginCatalogWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := a.GetPublicationEpoch(ctx)
	if err != nil || before != after {
		t.Fatalf("epoch %d -> %d error=%v", before, after, err)
	}
}

func TestCatalogBatchImportRollsBackAllRows(t *testing.T) {
	a, _ := catalogStorePair(t)
	ctx := context.Background()
	first := componentFixture("batch-first", "component-owner-a")
	second := componentFixture("batch-second", "component-owner-a")
	good := releaseFixture("batch-first-r1", first.ID, "1.0.0", domain.ReleaseDraft)
	bad := releaseFixture("batch-second-r1", second.ID, "1.0.0", domain.ReleaseDraft)
	bad.EnvironmentConstraints = map[string]any{"architecture": []string{"missing"}}
	event := catalogAudit("batch-audit", "import")
	err := a.CreateComponentImport(ctx, []domain.Component{first, second}, []domain.ComponentRelease{good, bad}, []domain.AuditEvent{event})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("invalid batch=%v", err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := a.GetComponent(ctx, id, true); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("partial component %s: %v", id, err)
		}
	}
	var count int
	if err := a.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE id=?", event.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial audit count=%d err=%v", count, err)
	}
}

func TestEnvironmentParameterFinalWriteRevalidatesContract(t *testing.T) {
	a, b := catalogStorePair(t)
	ctx := context.Background()
	c := componentFixture("field-component", "component-owner-a")
	if err := a.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("field-release", c.ID, "1.0.0", domain.ReleaseDraft)
	r.Parameters = []domain.ParameterDefinition{{Name: "region", Type: domain.ParameterTypeString, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}}}
	if err := a.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{domain.EnvironmentParameterValueKey(r.ID, r.Parameters[0]): "north"}
	snapshot, err := a.ReadCatalogValidation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ValidateValues(values, nil, nil); err != nil {
		t.Fatal(err)
	}
	r.Parameters = nil
	if err := b.UpdateDraftRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	e := domain.Environment{ID: "field-env", Name: "Environment", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	rev := domain.EnvironmentRevision{ID: "field-env-r1", EnvironmentID: e.ID, Revision: 1, Inventory: json.RawMessage(`{"hosts":[]}`), Parameters: values, CreatedAt: testNow}
	if err := a.CreateEnvironment(ctx, e, rev); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale field write=%v", err)
	}
	if _, err := a.GetEnvironment(ctx, e.ID, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("partial environment=%v", err)
	}
}

func TestCandidateRejectsUnapprovedAndStaleDigest(t *testing.T) {
	a, _ := catalogStorePair(t)
	ctx := context.Background()
	c := componentFixture("visibility", "component-owner-a")
	if err := a.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := releaseFixture("visibility-r1", c.ID, "1.0.0", domain.ReleaseDraft)
	if err := a.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, _ = a.GetComponentRelease(ctx, r.ID)
	if err := a.SetReleaseCandidate(ctx, r.ID, true, r.PublicationGeneration, "stale"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale digest=%v", err)
	}
	for _, status := range []domain.ReleaseReviewStatus{domain.ReleaseReviewNotSubmitted, domain.ReleaseReviewPending, domain.ReleaseReviewRejected, domain.ReleaseReviewApproved} {
		if _, err := a.DB().ExecContext(ctx, "UPDATE component_releases SET candidate=1,review_status=?,review_contract_digest='stale' WHERE id=?", status, r.ID); err != nil {
			t.Fatal(err)
		}
		visible, err := a.ListComponents(ctx, domain.User{ID: "scenario-owner-a", Role: domain.RoleScenarioOwner})
		if err != nil || len(visible) != 0 {
			t.Fatalf("invalid candidate visible=%+v err=%v", visible, err)
		}
	}
}
