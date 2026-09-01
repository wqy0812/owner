package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

var testNow = time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, u := range []domain.User{
		{ID: "component-owner-a", Name: "Owner A", Role: domain.RoleComponentOwner, CreatedAt: testNow},
		{ID: "component-owner-b", Name: "Owner B", Role: domain.RoleComponentOwner, CreatedAt: testNow},
		{ID: "scenario-owner-a", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: testNow},
		{ID: "environment-owner-a", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: testNow},
	} {
		if err := s.UpsertUser(context.Background(), u); err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}
	}
	return s
}

func componentFixture(id, owner string) domain.Component {
	return domain.Component{
		ID: id, Slug: id, Name: id, Layer: domain.LayerRuntimeState, Tags: []string{"runtime"},
		OwnerID: owner, CreatedAt: testNow, UpdatedAt: testNow,
	}
}

func releaseFixture(id, component, version string, status domain.ReleaseStatus) domain.ComponentRelease {
	r := domain.ComponentRelease{
		ID: id, ComponentID: component, Version: version,
		Status: status, RiskLevel: domain.RiskLow, CreatedAt: testNow,
		EnvironmentConstraints: map[string]any{"architecture": "amd64"},
		Parameters:             []domain.ParameterDefinition{},
		Actions: []domain.ActionDefinition{{
			ID: id + "-install", Name: "install", Kind: domain.ActionInstall,
			Playbook: "fixtures/install.yml", HostGroup: "workers", TimeoutSeconds: 60, RiskLevel: domain.RiskLow,
			RequiredCredentials: []string{"ansible_ssh_pass", "registry_user"}, Idempotent: true,
		}},
	}
	if status == domain.ReleaseReleased {
		r.ReleasedAt = ptr(testNow)
	}
	return r
}

func ptr[T any](v T) *T { return &v }

func releasePublicationGuard(release domain.ComponentRelease) ReleasePublicationGuard {
	return ReleasePublicationGuard{
		ReleaseID: release.ID, PublicationGeneration: release.PublicationGeneration,
		SpecDigest: domain.ComponentReleaseSpecDigest(release), Status: release.Status, Candidate: release.Candidate,
	}
}

func scenarioPublicationGuard(revision domain.ScenarioRevision) ScenarioPublicationGuard {
	return ScenarioPublicationGuard{
		RevisionID: revision.ID, PublicationGeneration: revision.PublicationGeneration,
		SpecDigest: domain.ScenarioRevisionSpecDigest(revision),
	}
}

func publicationEpoch(t *testing.T, s *Store) int64 {
	t.Helper()
	epoch, err := s.GetPublicationEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

func createSuccessfulScenarioTestEvidence(t *testing.T, s *Store, revision domain.ScenarioRevision, releases ...domain.ComponentRelease) string {
	t.Helper()
	ctx := context.Background()
	environmentID := "environment-" + revision.ID
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{}})
	environment := domain.Environment{ID: environmentID, Name: environmentID, OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	environmentRevision := domain.EnvironmentRevision{
		ID: environmentID + "-r1", EnvironmentID: environmentID, Revision: 1,
		Facts: map[string]any{}, Inventory: inventory, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, CreatedAt: testNow,
	}
	if err := s.CreateEnvironment(ctx, environment, environmentRevision); err != nil {
		t.Fatal(err)
	}
	steps := make([]any, 0, len(releases))
	for _, release := range releases {
		steps = append(steps, map[string]any{
			"id": release.ID + "-step", "nodeId": release.ID, "releaseId": release.ID,
			"releaseSpecDigest": domain.ComponentReleaseSpecDigest(release),
		})
	}
	runID := "run-" + revision.ID
	run := domain.Run{
		ID: runID, Kind: domain.RunScenarioTest, Status: domain.RunSucceeded,
		RequestedBy: "scenario-owner-a", EnvironmentID: environmentID, EnvironmentRevisionID: environmentRevision.ID,
		ScenarioRevisionID: revision.ID, CreatedAt: testNow,
		InputSnapshot: map[string]any{
			"scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision),
			"steps":                      steps,
		},
	}
	if err := s.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	return runID
}

func TestListRunsForComponentReleaseUsesImmutableRunEvidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	component := componentFixture("evidence-runtime", "component-owner-a")
	otherComponent := componentFixture("evidence-other", "component-owner-b")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponent(ctx, otherComponent); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("evidence-runtime-r1", component.ID, "1.0.0", domain.ReleaseReleased)
	otherRelease := releaseFixture("evidence-other-r1", otherComponent.ID, "1.0.0", domain.ReleaseReleased)
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, otherRelease); err != nil {
		t.Fatal(err)
	}
	inventory, _ := json.Marshal(map[string]any{"all": map[string]any{"hosts": map[string]any{}}})
	environment := domain.Environment{ID: "evidence-lab", Name: "Evidence Lab", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	environmentRevision := domain.EnvironmentRevision{ID: "evidence-lab-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: inventory, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, environment, environmentRevision); err != nil {
		t.Fatal(err)
	}
	scenario := domain.Scenario{ID: "evidence-scenario", Slug: "evidence-scenario", Name: "Evidence Scenario", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "evidence-scenario-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}

	create := func(run domain.Run) {
		t.Helper()
		if err := s.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	base := domain.Run{RequestedBy: "component-owner-a", EnvironmentID: environment.ID, EnvironmentRevisionID: environmentRevision.ID, InputSnapshot: map[string]any{}, CreatedAt: testNow}
	componentRun := base
	componentRun.ID, componentRun.Kind, componentRun.Status, componentRun.ComponentReleaseID = "evidence-component", domain.RunComponentTest, domain.RunSucceeded, release.ID
	create(componentRun)
	scenarioTest := base
	scenarioTest.ID, scenarioTest.Kind, scenarioTest.Status, scenarioTest.RequestedBy, scenarioTest.ScenarioRevisionID, scenarioTest.CreatedAt = "evidence-scenario-test", domain.RunScenarioTest, domain.RunFailed, "scenario-owner-a", revision.ID, testNow.Add(time.Minute)
	scenarioTest.InputSnapshot = map[string]any{"steps": []any{map[string]any{"releaseId": release.ID}, map[string]any{"releaseId": release.ID}, map[string]any{"releaseId": otherRelease.ID}}}
	create(scenarioTest)
	scenarioRun := base
	scenarioRun.ID, scenarioRun.Kind, scenarioRun.Status, scenarioRun.RequestedBy, scenarioRun.ScenarioRevisionID, scenarioRun.CreatedAt = "evidence-scenario-run", domain.RunScenario, domain.RunCancelled, "scenario-owner-a", revision.ID, testNow.Add(2*time.Minute)
	scenarioRun.InputSnapshot = map[string]any{"steps": []any{map[string]any{"releaseId": release.ID}}}
	create(scenarioRun)
	unrelated := base
	unrelated.ID, unrelated.Kind, unrelated.Status, unrelated.ComponentReleaseID, unrelated.CreatedAt = "evidence-unrelated", domain.RunComponentTest, domain.RunSucceeded, otherRelease.ID, testNow.Add(3*time.Minute)
	create(unrelated)
	rollback := base
	rollback.ID, rollback.Kind, rollback.Status, rollback.ComponentReleaseID, rollback.CreatedAt = "evidence-rollback", domain.RunEnvironmentRollback, domain.RunSucceeded, release.ID, testNow.Add(4*time.Minute)
	rollback.InputSnapshot = map[string]any{"steps": []any{map[string]any{"releaseId": release.ID}}}
	create(rollback)

	runs, err := s.ListRunsForComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"evidence-scenario-run", "evidence-scenario-test", "evidence-component"}
	if len(runs) != len(want) {
		t.Fatalf("release evidence runs=%+v, want ids=%v", runs, want)
	}
	for index, id := range want {
		if runs[index].ID != id {
			t.Fatalf("release evidence order[%d]=%s, want %s", index, runs[index].ID, id)
		}
	}
}

func TestComponentVisibilityAndReleaseImmutability(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for _, c := range []domain.Component{componentFixture("alice-private", "component-owner-a"), componentFixture("bob-private", "component-owner-b"), componentFixture("bob-public", "component-owner-b")} {
		if err := s.CreateComponent(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("alice-draft", "alice-private", "1.0.0", domain.ReleaseDraft)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("bob-draft", "bob-private", "1.0.0", domain.ReleaseDraft)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("bob-release", "bob-public", "1.0.0", domain.ReleaseReleased)); err != nil {
		t.Fatal(err)
	}

	alice, _ := s.GetUser(ctx, "component-owner-a")
	got, err := s.ListComponents(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Owner A sees %d components, want own private plus Owner B public", len(got))
	}
	for _, c := range got {
		if c.ID == "bob-private" {
			t.Fatal("Owner A can see Owner B's draft-only component")
		}
		if c.ID == "bob-public" && len(c.Releases) != 1 {
			t.Fatalf("expected one released Owner B version, got %d", len(c.Releases))
		}
	}

	draft, _ := s.GetComponentRelease(ctx, "alice-draft")
	if got := draft.Actions[0].RequiredCredentials; !reflect.DeepEqual(got, []string{"ansible_ssh_pass", "registry_user"}) {
		t.Fatalf("required credentials round trip=%v", got)
	}
	if !draft.Actions[0].Idempotent {
		t.Fatal("idempotent install capability was not persisted")
	}
	if err := s.PublishComponentRelease(ctx, draft.ID, publicationEpoch(t, s), []ReleasePublicationGuard{releasePublicationGuard(draft)}, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	draft.ReleaseNotes = "illegal mutation"
	if err := s.UpdateDraftRelease(ctx, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released update = %v, want conflict", err)
	}
}

func TestCandidateReleaseVisibilityAndAtomicScenarioPublish(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("candidate-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("candidate-runtime-1", component.ID, "1.0.0-rc1", domain.ReleaseDraft)
	release.Candidate = true
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	viewer, _ := s.GetUser(ctx, "scenario-owner-a")
	visible, err := s.ListComponents(ctx, viewer)
	if err != nil || len(visible) != 1 || len(visible[0].Releases) != 1 || !visible[0].Releases[0].Candidate {
		t.Fatalf("candidate visibility=%+v err=%v", visible, err)
	}
	if err := s.SetReleaseCandidate(ctx, release.ID, false); err != nil {
		t.Fatal(err)
	}
	invalidated, _ := s.GetComponentRelease(ctx, release.ID)
	if invalidated.Candidate {
		t.Fatalf("candidate handoff was not withdrawn: %+v", invalidated)
	}
	visible, err = s.ListComponents(ctx, viewer)
	if err != nil || len(visible) != 0 {
		t.Fatalf("invalidated candidate remained visible=%+v err=%v", visible, err)
	}
	if err := s.SetReleaseCandidate(ctx, release.ID, true); err != nil {
		t.Fatal(err)
	}
	release, _ = s.GetComponentRelease(ctx, release.ID)
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", ReleaseID: release.ID, Action: domain.ActionInstall, HostGroup: "all"}}, Edges: []domain.ScenarioEdge{}}
	scenario := domain.Scenario{ID: "candidate-scene", Slug: "candidate-scene", Name: "Candidate", OwnerID: viewer.ID, CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "candidate-scene-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTestPassed, Graph: graph, CreatedAt: testNow, TestPassedAt: ptr(testNow)}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	revision, _ = s.GetScenarioRevision(ctx, revision.ID)
	evidenceRunID := createSuccessfulScenarioTestEvidence(t, s, revision, release)
	if err := s.PublishCandidateReleaseSet(ctx, scenarioPublicationGuard(revision), []string{release.ID}, []ReleasePublicationGuard{releasePublicationGuard(release)}, evidenceRunID, publicationEpoch(t, s), testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotRelease, _ := s.GetComponentRelease(ctx, release.ID)
	gotRevision, _ := s.GetScenarioRevision(ctx, revision.ID)
	if gotRelease.Status != domain.ReleaseReleased || gotRelease.Candidate || gotRevision.Status != domain.RevisionReleased {
		t.Fatalf("atomic publish release=%+v revision=%+v", gotRelease, gotRevision)
	}
}

func TestCandidateReleaseSetPublishRollsBackOnStaleMember(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for _, id := range []string{"candidate-a", "candidate-b"} {
		if err := s.CreateComponent(ctx, componentFixture(id, "component-owner-a")); err != nil {
			t.Fatal(err)
		}
		release := releaseFixture(id+"-r1", id, "1.0.0-rc1", domain.ReleaseDraft)
		release.Candidate = true
		if err := s.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	releaseA, _ := s.GetComponentRelease(ctx, "candidate-a-r1")
	releaseB, _ := s.GetComponentRelease(ctx, "candidate-b-r1")
	scenario := domain.Scenario{ID: "stale-set", Slug: "stale-set", Name: "Stale", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "stale-set-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTestPassed, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "a", ReleaseID: releaseA.ID, Action: domain.ActionInstall},
		{ID: "b", ReleaseID: releaseB.ID, Action: domain.ActionInstall},
	}, Edges: []domain.ScenarioEdge{}}, CreatedAt: testNow, TestPassedAt: ptr(testNow)}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	revision, _ = s.GetScenarioRevision(ctx, revision.ID)
	evidenceRunID := createSuccessfulScenarioTestEvidence(t, s, revision, releaseA, releaseB)
	guards := []ReleasePublicationGuard{releasePublicationGuard(releaseA), releasePublicationGuard(releaseB)}
	epoch := publicationEpoch(t, s)
	if err := s.SetReleaseCandidate(ctx, releaseB.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReleaseCandidate(ctx, releaseB.ID, true); err != nil {
		t.Fatal(err)
	}
	err := s.PublishCandidateReleaseSet(ctx, scenarioPublicationGuard(revision), []string{releaseA.ID, releaseB.ID}, guards, evidenceRunID, epoch, testNow.Add(time.Minute))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale candidate set error=%v", err)
	}
	first, _ := s.GetComponentRelease(ctx, "candidate-a-r1")
	gotRevision, _ := s.GetScenarioRevision(ctx, revision.ID)
	if first.Status != domain.ReleaseDraft || gotRevision.Status != domain.RevisionTestPassed {
		t.Fatalf("partial publish escaped transaction release=%s revision=%s", first.Status, gotRevision.Status)
	}
}

func TestStandalonePublishRejectsDefinitionChangedAfterValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("guarded-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("guarded-runtime-r1", component.ID, "1.0.0", domain.ReleaseDraft)
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, _ = s.GetComponentRelease(ctx, release.ID)
	guard := releasePublicationGuard(release)
	epoch := publicationEpoch(t, s)
	release.ReleaseNotes = "changed after validation"
	if err := s.UpdateDraftRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishComponentRelease(ctx, release.ID, epoch, []ReleasePublicationGuard{guard}, testNow.Add(time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale guarded publish error=%v", err)
	}
	current, _ := s.GetComponentRelease(ctx, release.ID)
	if current.Status != domain.ReleaseDraft {
		t.Fatalf("stale definition was published: %+v", current)
	}
}

func TestArtifactSourceRepairDoesNotAdvancePublicationGeneration(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("source-stable-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("source-stable-runtime-r1", component.ID, "1.0.0", domain.ReleaseDraft)
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	artifact := domain.ComponentArtifact{
		ID: "source-stable-artifact", ReleaseID: release.ID, Alias: "package", Filename: "package.tgz", SHA256: strings.Repeat("a", 64),
		SourceURL: "https://example.invalid/one", SourceUpdatedBy: "component-owner-a", SourceUpdatedAt: testNow,
		CreatedBy: "component-owner-a", CreatedAt: testNow,
	}
	if err := s.UpsertDraftComponentArtifactAndInvalidate(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetComponentRelease(ctx, release.ID)
	if _, err := s.UpdateComponentArtifactSource(ctx, release.ID, artifact.Alias, "https://example.invalid/two", "component-owner-a", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	afterSource, _ := s.GetComponentRelease(ctx, release.ID)
	if afterSource.PublicationGeneration != before.PublicationGeneration {
		t.Fatalf("source-only repair advanced publication generation: before=%d after=%d", before.PublicationGeneration, afterSource.PublicationGeneration)
	}
	artifact.SHA256 = strings.Repeat("b", 64)
	artifact.SourceURL = "https://example.invalid/three"
	if err := s.UpsertDraftComponentArtifactAndInvalidate(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	afterIdentity, _ := s.GetComponentRelease(ctx, release.ID)
	if afterIdentity.PublicationGeneration <= afterSource.PublicationGeneration {
		t.Fatalf("content identity change did not advance publication generation: before=%d after=%d", afterSource.PublicationGeneration, afterIdentity.PublicationGeneration)
	}
}

func TestImageSourceRepairPreservesIdentityWhileDigestChangesInvalidate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("image-source-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("image-source-runtime-r1", component.ID, "1.0.0", domain.ReleaseDraft)
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	image := domain.ComponentImage{
		ID: "image-source-main", ReleaseID: release.ID, LogicalName: "main", Digest: "sha256:" + strings.Repeat("a", 64),
		SourceRef: "registry-one.invalid/runtime:1.0.0", SourceUpdatedBy: "component-owner-a", SourceUpdatedAt: testNow,
		CreatedBy: "component-owner-a", CreatedAt: testNow,
	}
	if err := s.UpsertDraftComponentImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateComponentImageSource(ctx, release.ID, image.LogicalName, "registry-two.invalid/runtime:1.0.0", "component-owner-b", testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	afterSource, _ := s.GetComponentRelease(ctx, release.ID)
	if updated.Digest != image.Digest || updated.SourceRef != "registry-two.invalid/runtime:1.0.0" || updated.SourceUpdatedBy != "component-owner-b" {
		t.Fatalf("updated image=%+v", updated)
	}
	if afterSource.PublicationGeneration != before.PublicationGeneration {
		t.Fatalf("source-only repair advanced publication generation: before=%d after=%d", before.PublicationGeneration, afterSource.PublicationGeneration)
	}

	image.Digest = "sha256:" + strings.Repeat("b", 64)
	image.SourceRef = "registry-three.invalid/runtime:1.0.0"
	if err := s.UpsertDraftComponentImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	afterIdentity, _ := s.GetComponentRelease(ctx, release.ID)
	if afterIdentity.PublicationGeneration <= afterSource.PublicationGeneration {
		t.Fatalf("digest change did not advance publication generation: before=%d after=%d", afterSource.PublicationGeneration, afterIdentity.PublicationGeneration)
	}
	images, err := s.ListComponentImages(ctx, release.ID)
	if err != nil || len(images) != 1 || images[0].Digest != image.Digest {
		t.Fatalf("images=%+v err=%v", images, err)
	}
	if err := s.DeleteDraftComponentImage(ctx, release.ID, image.LogicalName); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetComponentImage(ctx, release.ID, image.LogicalName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted image lookup error=%v", err)
	}
	if err := s.DeleteDraftComponentImage(ctx, release.ID, image.LogicalName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("repeated image delete error=%v", err)
	}
}

func TestArtifactMirrorRecordIsIdempotentByTargetAndIdentity(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	checksum := strings.Repeat("c", 64)
	present, err := s.HasComponentArtifactMirror(ctx, "target-fss", "components/runtime.tgz", checksum)
	if err != nil || present {
		t.Fatalf("unexpected initial mirror present=%v err=%v", present, err)
	}
	if err := s.RecordComponentArtifactMirror(ctx, "source-a", "target-fss", "components/runtime.tgz", checksum, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordComponentArtifactMirror(ctx, "source-b", "target-fss", "components/runtime.tgz", checksum, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	present, err = s.HasComponentArtifactMirror(ctx, "target-fss", "components/runtime.tgz", checksum)
	if err != nil || !present {
		t.Fatalf("recorded mirror present=%v err=%v", present, err)
	}
	other, err := s.HasComponentArtifactMirror(ctx, "target-fss", "components/runtime.tgz", strings.Repeat("d", 64))
	if err != nil || other {
		t.Fatalf("different identity mirror present=%v err=%v", other, err)
	}
}

func TestScenarioTestEvidenceRejectsChangedReleaseDefinition(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("tested-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("tested-runtime-r1", component.ID, "1.0.0-rc1", domain.ReleaseDraft)
	release.Candidate = true
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, _ = s.GetComponentRelease(ctx, release.ID)
	scenario := domain.Scenario{ID: "tested-scenario", Slug: "tested-scenario", Name: "Tested", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{
		ID: "tested-scenario-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTesting,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", ReleaseID: release.ID, Action: domain.ActionInstall}}, Edges: []domain.ScenarioEdge{}}, CreatedAt: testNow,
	}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	revision, _ = s.GetScenarioRevision(ctx, revision.ID)
	runID := createSuccessfulScenarioTestEvidence(t, s, revision, release)
	release.ReleaseNotes = "definition changed while the scenario test was running"
	if err := s.UpdateDraftRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := s.PromoteScenarioRevisionFromRun(ctx, runID, testNow.Add(time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale scenario evidence error=%v", err)
	}
	current, _ := s.GetScenarioRevision(ctx, revision.ID)
	if current.Status != domain.RevisionDraft || current.TestPassedAt != nil {
		t.Fatalf("stale scenario evidence was recorded: %+v", current)
	}
}

func TestScenarioPublishRejectsReleaseChangedAfterSuccessfulTest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := componentFixture("publish-tested-runtime", "component-owner-a")
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("publish-tested-runtime-r1", component.ID, "1.0.0-rc1", domain.ReleaseDraft)
	release.Candidate = true
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, _ = s.GetComponentRelease(ctx, release.ID)
	scenario := domain.Scenario{ID: "publish-tested-scenario", Slug: "publish-tested-scenario", Name: "Publish Tested", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{
		ID: "publish-tested-scenario-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTestPassed,
		Graph:     domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", ReleaseID: release.ID, Action: domain.ActionInstall}}, Edges: []domain.ScenarioEdge{}},
		CreatedAt: testNow, TestPassedAt: ptr(testNow),
	}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	revision, _ = s.GetScenarioRevision(ctx, revision.ID)
	evidenceRunID := createSuccessfulScenarioTestEvidence(t, s, revision, release)
	release.ReleaseNotes = "changed after the complete scenario test"
	if err := s.UpdateDraftRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	currentRelease, _ := s.GetComponentRelease(ctx, release.ID)
	err := s.PublishCandidateReleaseSet(
		ctx, scenarioPublicationGuard(revision), []string{release.ID},
		[]ReleasePublicationGuard{releasePublicationGuard(currentRelease)}, evidenceRunID, publicationEpoch(t, s), testNow.Add(time.Minute),
	)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale tested definition publish error=%v", err)
	}
	currentRevision, _ := s.GetScenarioRevision(ctx, revision.ID)
	currentRelease, _ = s.GetComponentRelease(ctx, release.ID)
	if currentRevision.Status != domain.RevisionTestPassed || currentRelease.Status != domain.ReleaseDraft {
		t.Fatalf("stale evidence escaped atomic publish: revision=%s release=%s", currentRevision.Status, currentRelease.Status)
	}
}

func TestEnvironmentRollbackFenceIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	newEnvironment := func(t *testing.T, s *Store, id string) domain.EnvironmentRevision {
		t.Helper()
		inventory, _ := json.Marshal(map[string]any{"hosts": []any{}})
		environment := domain.Environment{ID: id, Name: id, OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
		revision := domain.EnvironmentRevision{ID: id + "-r1", EnvironmentID: id, Revision: 1, Facts: map[string]any{}, Inventory: inventory, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, CreatedAt: testNow}
		if err := s.CreateEnvironment(ctx, environment, revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	active := func(id string, kind domain.RunKind, revision domain.EnvironmentRevision) domain.Run {
		return domain.Run{ID: id, Kind: kind, Status: domain.RunAwaitingApproval, RequestedBy: "environment-owner-a", EnvironmentID: revision.EnvironmentID, EnvironmentRevisionID: revision.ID, InputSnapshot: map[string]any{}, CreatedAt: testNow}
	}

	t.Run("rollback blocks another active run", func(t *testing.T) {
		s := newTestStore(t)
		revision := newEnvironment(t, s, "rollback-fence-first")
		if err := s.CreateRun(ctx, active("rollback-fence", domain.RunEnvironmentRollback, revision), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateRun(ctx, active("other-run", domain.RunComponentTest, revision), nil); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("active Run entered rollback-fenced environment: %v", err)
		}
	})

	t.Run("active run blocks rollback", func(t *testing.T) {
		s := newTestStore(t)
		revision := newEnvironment(t, s, "rollback-fence-second")
		if err := s.CreateRun(ctx, active("other-first", domain.RunScenario, revision), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateRun(ctx, active("rollback-second", domain.RunEnvironmentRollback, revision), nil); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("rollback entered environment with active Run: %v", err)
		}
	})
}

func TestBatchApprovalIsAtomicAndPreservesFIFOOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{}})
	environment := domain.Environment{ID: "batch-env", Name: "Batch", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.EnvironmentRevision{ID: "batch-env-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: inventory, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, environment, revision); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 2; index++ {
		run := domain.Run{ID: fmt.Sprintf("batch-run-%d", index), Kind: domain.RunScenario, Status: domain.RunAwaitingApproval, RequestedBy: "scenario-owner-a", EnvironmentID: environment.ID, EnvironmentRevisionID: revision.ID, Destructive: true, InputSnapshot: map[string]any{}, CreatedAt: testNow.Add(time.Duration(index) * time.Second)}
		approval := domain.Approval{ID: fmt.Sprintf("batch-approval-%d", index), RunID: run.ID, Status: "pending", RequestedAt: run.CreatedAt}
		if err := s.CreateRun(ctx, run, &approval); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.BatchDecideApprovals(ctx, []string{"batch-approval-1", "batch-approval-2"}, "environment-owner-a", "approved", "window", testNow.Add(time.Minute))
	if err != nil || len(runs) != 2 || runs[0].Status != domain.RunQueued || runs[1].Status != domain.RunQueued {
		t.Fatalf("batch approval runs=%+v err=%v", runs, err)
	}
	claimed, err := s.ClaimNextRun(ctx, environment.ID, testNow.Add(2*time.Minute))
	if err != nil || claimed.ID != "batch-run-1" {
		t.Fatalf("batch FIFO claim=%+v err=%v", claimed, err)
	}

	stale := domain.Run{ID: "batch-stale-run", Kind: domain.RunScenario, Status: domain.RunAwaitingApproval, RequestedBy: "scenario-owner-a", EnvironmentID: environment.ID, EnvironmentRevisionID: revision.ID, Destructive: true, InputSnapshot: map[string]any{}, CreatedAt: testNow.Add(3 * time.Minute)}
	staleApproval := domain.Approval{ID: "batch-stale-approval", RunID: stale.ID, Status: "pending", RequestedAt: stale.CreatedAt}
	if err := s.CreateRun(ctx, stale, &staleApproval); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BatchDecideApprovals(ctx, []string{"batch-stale-approval", "missing"}, "environment-owner-a", "approved", "window", testNow.Add(4*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale batch error=%v", err)
	}
	unchanged, _ := s.GetRun(ctx, stale.ID)
	if unchanged.Status != domain.RunAwaitingApproval {
		t.Fatalf("stale batch partially consumed run: %s", unchanged.Status)
	}
}

func TestComponentLayerDatabaseConstraint(t *testing.T) {
	s := newTestStore(t)
	component := componentFixture("invalid-classification", "component-owner-a")
	component.Layer = "unknown"
	if err := s.CreateComponent(context.Background(), component); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("invalid layer insert=%v, want conflict", err)
	}
}

func TestReleaseParametersAndMappingsRoundTripAndClone(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateComponent(ctx, componentFixture("kubelet", "component-owner-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponent(ctx, componentFixture("kube-proxy", "component-owner-a")); err != nil {
		t.Fatal(err)
	}
	minLength := 1
	_ = minLength
	upstream := releaseFixture("release-kubelet", "kubelet", "1.17.5", domain.ReleaseReleased)
	upstream.Parameters = []domain.ParameterDefinition{{
		Name: "kubeInstallRoot", Description: "kubelet install root", Type: domain.ParameterTypeString,
		Required: true, DefaultValue: "/approot1/paas/kube", Visibility: domain.ParameterPublic, MinLength: 1,
	}}
	if err := s.CreateComponentRelease(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	downstream := releaseFixture("release-kube-proxy", "kube-proxy", "1.17.5", domain.ReleaseDraft)
	downstream.Parameters = []domain.ParameterDefinition{{
		Name: "kubeRoot", Description: "imported kubelet root", Type: domain.ParameterTypeString,
		Required: true, Visibility: domain.ParameterInternal,
	}}
	downstream.Dependencies = []domain.ComponentDependency{{
		ID: "dependency-kube-proxy-kubelet", ReleaseID: downstream.ID,
		UpstreamComponentID: "kubelet", UpstreamReleaseID: upstream.ID, Purpose: "reuse install root",
		ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}},
	}}
	if err := s.CreateComponentRelease(ctx, downstream); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetComponentRelease(ctx, downstream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parameters) != 1 || got.Parameters[0].Name != "kubeRoot" || got.Parameters[0].Visibility != domain.ParameterInternal {
		t.Fatalf("parameters=%+v", got.Parameters)
	}
	if len(got.Dependencies) != 1 || len(got.Dependencies[0].ParameterMappings) != 1 || got.Dependencies[0].ParameterMappings[0].TargetParameter != "kubeRoot" {
		t.Fatalf("dependencies=%+v", got.Dependencies)
	}
	if got.Dependencies[0].UpstreamComponentName != "kubelet" || got.Dependencies[0].UpstreamVersion != "1.17.5" {
		t.Fatalf("upstream identity=%+v", got.Dependencies[0])
	}

	cloned := got
	cloned.ID = "release-kube-proxy-clone"
	cloned.LineID = "line-release-kube-proxy-clone"
	cloned.LineName = "Independent 1.17.6"
	cloned.ParentReleaseID = ""
	cloned.TemplateSourceReleaseID = got.ID
	cloned.Compatibility = domain.CompatibilityNotApplicable
	cloned.Version = "1.17.6"
	cloned.Status = domain.ReleaseDraft
	cloned.Dependencies[0].ID = "dependency-clone"
	cloned.Dependencies[0].ReleaseID = cloned.ID
	for i := range cloned.Actions {
		cloned.Actions[i].ID = cloned.ID + "-action-" + string(rune('a'+i))
		cloned.Actions[i].ReleaseID = cloned.ID
	}
	if err := s.CreateComponentRelease(ctx, cloned); err != nil {
		t.Fatal(err)
	}
	copy, err := s.GetComponentRelease(ctx, cloned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copy.Parameters, got.Parameters) || !reflect.DeepEqual(copy.Dependencies[0].ParameterMappings, got.Dependencies[0].ParameterMappings) {
		t.Fatalf("clone parameters=%+v mappings=%+v", copy.Parameters, copy.Dependencies[0].ParameterMappings)
	}
}

func TestSQLiteConnectionPragmasApplyAcrossPool(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	connections := make([]*sql.Conn, 0, 8)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		connection, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("connection %d pragmas foreign_keys=%d busy_timeout=%d", i, foreignKeys, busyTimeout)
		}
	}
}

func TestIsSQLiteBusyRecognizesSnapshotAndLockErrors(t *testing.T) {
	for _, message := range []string{"database is locked (517)", "SQLITE_BUSY: database is locked"} {
		if !isSQLiteBusy(errors.New(message)) {
			t.Fatalf("expected %q to be treated as retryable SQLite contention", message)
		}
	}
	if isSQLiteBusy(errors.New("constraint failed")) {
		t.Fatal("non-locking SQLite failures must not be retried")
	}
}

func TestScenarioEnvironmentRunApprovalAndFIFO(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateComponent(ctx, componentFixture("runtime", "component-owner-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("runtime-1", "runtime", "1.0.0", domain.ReleaseReleased)); err != nil {
		t.Fatal(err)
	}

	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", Name: "Runtime", ReleaseID: "runtime-1", Action: domain.ActionInstall, HostGroup: "workers", Values: map[string]any{}}}, Edges: []domain.ScenarioEdge{}}
	scenario := domain.Scenario{ID: "cluster", Slug: "cluster", Name: "Cluster", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "cluster-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: graph, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, testNow); err != nil {
		t.Fatal(err)
	}

	inventory, _ := json.Marshal(map[string]any{"all": map[string]any{"hosts": map[string]any{"localhost": map[string]any{"ansible_connection": "local"}}}})
	env := domain.Environment{ID: "lab", Name: "Lab", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	envRev := domain.EnvironmentRevision{ID: "lab-r1", EnvironmentID: env.ID, Revision: 1, Facts: map[string]any{"architecture": "amd64"}, Inventory: inventory, Variables: map[string]string{"IMAGE_REGISTRY": "registry.example.test:5000"}, CredentialRefs: []domain.CredentialRef{{Name: "ssh", Kind: "envVarRef", Reference: "TEST_KEY"}}, CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, env, envRev); err != nil {
		t.Fatal(err)
	}
	storedEnvironment, err := s.GetEnvironment(ctx, env.ID, false)
	if err != nil || storedEnvironment.Revision == nil || storedEnvironment.Revision.Variables["IMAGE_REGISTRY"] != "registry.example.test:5000" {
		t.Fatalf("stored environment variables=%+v err=%v", storedEnvironment.Revision, err)
	}

	approval := domain.Approval{ID: "approval-1", RunID: "run-1", Status: "pending", RequestedAt: testNow}
	run1 := domain.Run{ID: "run-1", Kind: domain.RunScenarioTest, Status: domain.RunAwaitingApproval, RequestedBy: "scenario-owner-a", EnvironmentID: env.ID, EnvironmentRevisionID: envRev.ID, ScenarioRevisionID: revision.ID, Destructive: true, InputSnapshot: map[string]any{"steps": []any{map[string]any{"releaseId": "runtime-1"}}}, CreatedAt: testNow}
	if err := s.CreateRun(ctx, run1, &approval); err != nil {
		t.Fatal(err)
	}
	for index, message := range []string{"one", "two", "three", "four", "five"} {
		if _, err := s.AppendRunLog(ctx, domain.RunLog{RunID: run1.ID, Stream: "stdout", Message: message, CreatedAt: testNow.Add(time.Duration(index) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	logTail, err := s.ListRunLogTail(ctx, run1.ID, 2)
	if err != nil || len(logTail) != 2 || logTail[0].Message != "four" || logTail[1].Message != "five" {
		t.Fatalf("run log tail=%+v err=%v", logTail, err)
	}
	for _, test := range []struct {
		userID string
		want   bool
	}{
		{userID: "scenario-owner-a", want: true},
		{userID: "environment-owner-a", want: true},
		{userID: "component-owner-a", want: true},
		{userID: "component-owner-b", want: false},
	} {
		viewer, _ := s.GetUser(ctx, test.userID)
		visible, err := s.CanViewRun(ctx, viewer, run1.ID)
		if err != nil || visible != test.want {
			t.Fatalf("CanViewRun(%s)=%v err=%v, want %v", test.userID, visible, err, test.want)
		}
	}
	if err := s.DecideApproval(ctx, approval.ID, "environment-owner-a", "approved", "safe lab", testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	run2 := run1
	run2.ID = "run-2"
	run2.Status = domain.RunQueued
	run2.Destructive = false
	run2.CreatedAt = testNow.Add(time.Minute)
	if err := s.CreateRun(ctx, run2, nil); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimNextRun(ctx, env.ID, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "run-1" {
		t.Fatalf("claimed %s, want FIFO run-1", claimed.ID)
	}
	if _, err := s.ClaimNextRun(ctx, env.ID, testNow.Add(3*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second claim=%v, want environment conflict", err)
	}
	if err := s.UpdateRunStatus(ctx, claimed.ID, []domain.RunStatus{domain.RunRunning}, domain.RunSucceeded, "", testNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimNextRun(ctx, env.ID, testNow.Add(5*time.Minute))
	if err != nil || claimed.ID != "run-2" {
		t.Fatalf("second FIFO claim=%+v err=%v", claimed, err)
	}

	cancelledRun := run1
	cancelledRun.ID = "run-cancelled"
	cancelledRun.Status = domain.RunAwaitingApproval
	cancelledRun.CreatedAt = testNow.Add(10 * time.Minute)
	cancelledApproval := domain.Approval{ID: "approval-cancelled", RunID: cancelledRun.ID, Status: "pending", RequestedAt: cancelledRun.CreatedAt}
	if err := s.CreateRun(ctx, cancelledRun, &cancelledApproval); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRunStatus(ctx, cancelledRun.ID, []domain.RunStatus{domain.RunAwaitingApproval}, domain.RunCancelled, "cancelled", testNow.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideApproval(ctx, cancelledApproval.ID, "environment-owner-a", "approved", "too late", testNow.Add(12*time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("approval of cancelled run = %v, want conflict", err)
	}
	gotApproval, err := s.GetApproval(ctx, cancelledApproval.ID)
	if err != nil || gotApproval.Status != "pending" {
		t.Fatalf("cancelled approval mutated despite rollback: %+v err=%v", gotApproval, err)
	}
	componentTest := domain.Run{
		ID: "component-test-active", Kind: domain.RunComponentTest, Status: domain.RunQueued,
		RequestedBy: "component-owner-a", EnvironmentID: env.ID, EnvironmentRevisionID: envRev.ID,
		ComponentReleaseID: "runtime-1", InputSnapshot: map[string]any{}, CreatedAt: testNow.Add(20 * time.Minute),
	}
	if err := s.CreateRun(ctx, componentTest, nil); err != nil {
		t.Fatal(err)
	}
	if active, err := s.HasActiveComponentTest(ctx, componentTest.ComponentReleaseID); err != nil || !active {
		t.Fatalf("active component test=%v err=%v", active, err)
	}
	if err := s.UpdateRunStatus(ctx, componentTest.ID, []domain.RunStatus{domain.RunQueued}, domain.RunCancelled, "cancelled", testNow.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active, err := s.HasActiveComponentTest(ctx, componentTest.ComponentReleaseID); err != nil || active {
		t.Fatalf("terminal component test active=%v err=%v", active, err)
	}
}

func TestEmptyImageBuildListsEncodeAsArrays(t *testing.T) {
	s := newTestStore(t)
	builds, err := s.ListComponentImageBuilds(context.Background(), "missing-release", 20)
	if err != nil || builds == nil || len(builds) != 0 {
		t.Fatalf("empty builds=%#v err=%v", builds, err)
	}
	logs, err := s.ListComponentImageBuildLogs(context.Background(), "missing-build", 20)
	if err != nil || logs == nil || len(logs) != 0 {
		t.Fatalf("empty build logs=%#v err=%v", logs, err)
	}
}

func TestFailInvalidActiveRunsKeepsCorruptEntriesOutOfQueue(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateComponent(ctx, componentFixture("reconcile-component", "component-owner-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateComponentRelease(ctx, releaseFixture("reconcile-release", "reconcile-component", "1.0.0", domain.ReleaseReleased)); err != nil {
		t.Fatal(err)
	}
	environment := domain.Environment{ID: "reconcile-env", Name: "Reconcile", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.EnvironmentRevision{ID: "reconcile-env-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, environment, revision); err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{"steps": []any{map[string]any{"releaseId": "reconcile-release"}}}
	invalid := domain.Run{ID: "invalid-awaiting", Kind: domain.RunComponentTest, Status: domain.RunAwaitingApproval, RequestedBy: "component-owner-a", EnvironmentID: environment.ID, EnvironmentRevisionID: revision.ID, ComponentReleaseID: "reconcile-release", Destructive: true, InputSnapshot: plan, CreatedAt: testNow}
	if err := s.CreateRun(ctx, invalid, nil); err != nil {
		t.Fatal(err)
	}
	valid := invalid
	valid.ID = "valid-awaiting"
	valid.CreatedAt = testNow.Add(time.Second)
	approval := domain.Approval{ID: "valid-approval", RunID: valid.ID, Status: "pending", RequestedAt: valid.CreatedAt}
	if err := s.CreateRun(ctx, valid, &approval); err != nil {
		t.Fatal(err)
	}

	count, err := s.FailInvalidActiveRuns(ctx, testNow.Add(time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("reconciled count=%d err=%v", count, err)
	}
	failed, _ := s.GetRun(ctx, invalid.ID)
	kept, _ := s.GetRun(ctx, valid.ID)
	if failed.Status != domain.RunFailed || !strings.Contains(failed.Error, "invalid active run") || kept.Status != domain.RunAwaitingApproval {
		t.Fatalf("reconciled invalid=%+v valid=%+v", failed, kept)
	}
}

func TestRestartInterruptsRunAndReleasesScenarioTestingState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	component := domain.Component{ID: "restart-component", Slug: "restart-component", Name: "Restart Component", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: "component-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	if err := s.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	releasedAt := testNow
	release := releaseFixture("restart-release", component.ID, "1.0.0", domain.ReleaseReleased)
	release.ReleasedAt = &releasedAt
	if err := s.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	scenario := domain.Scenario{ID: "restart-scenario", Slug: "restart-scenario", Name: "Restart", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	revision := domain.ScenarioRevision{ID: "restart-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionTesting, Graph: domain.ScenarioGraph{}, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	environment := domain.Environment{ID: "restart-env", Name: "Restart Env", OwnerID: "environment-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	environmentRevision := domain.EnvironmentRevision{ID: "restart-env-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: testNow}
	if err := s.CreateEnvironment(ctx, environment, environmentRevision); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "restart-run", Kind: domain.RunScenarioTest, Status: domain.RunRunning, RequestedBy: "scenario-owner-a", EnvironmentID: environment.ID, EnvironmentRevisionID: environmentRevision.ID, ScenarioRevisionID: revision.ID, InputSnapshot: map[string]any{"steps": []any{map[string]any{"id": "step", "releaseId": release.ID}}}, CreatedAt: testNow}
	if err := s.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	started := testNow.Add(30 * time.Second)
	step := domain.RunStep{ID: "restart-step", RunID: run.ID, NodeID: "node", Name: "running", Status: domain.RunRunning, StartedAt: &started}
	if err := s.CreateRunStep(ctx, step); err != nil {
		t.Fatal(err)
	}
	if count, err := s.MarkRunningInterrupted(ctx, testNow.Add(time.Minute)); err != nil || count != 1 {
		t.Fatalf("MarkRunningInterrupted count=%d err=%v", count, err)
	}
	gotRun, _ := s.GetRun(ctx, run.ID)
	gotRevision, _ := s.GetScenarioRevision(ctx, revision.ID)
	steps, _ := s.ListRunSteps(ctx, run.ID)
	if gotRun.Status != domain.RunInterrupted || gotRevision.Status != domain.RevisionDraft || len(steps) != 1 || steps[0].Status != domain.RunInterrupted || steps[0].FinishedAt == nil {
		t.Fatalf("restart state run=%s revision=%s steps=%+v", gotRun.Status, gotRevision.Status, steps)
	}
}

func TestNotificationsAuditSessionAndLogs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateSession(ctx, "hash", "component-owner-a", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if u, err := s.UserBySession(ctx, "hash"); err != nil || u.ID != "component-owner-a" {
		t.Fatalf("session user=%+v err=%v", u, err)
	}

	n := domain.Notification{ID: "n1", UserID: "component-owner-a", Type: "component_released", Title: "Runtime updated", Body: "1.1.0", Payload: map[string]any{"path": []any{"runtime", "kubernetes"}}, CreatedAt: testNow}
	if err := s.CreateNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListNotifications(ctx, n.UserID, true); err != nil || len(got) != 1 {
		t.Fatalf("notifications=%+v err=%v", got, err)
	}
	if err := s.MarkNotificationRead(ctx, n.ID, n.UserID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListNotifications(ctx, n.UserID, true); len(got) != 0 {
		t.Fatalf("unread notifications=%d", len(got))
	}

	e := domain.AuditEvent{ID: "audit-1", ActorID: n.UserID, Action: "component.release", ResourceType: "component_release", ResourceID: "r1", Metadata: map[string]any{"secret": "[REDACTED]"}, CreatedAt: testNow}
	if err := s.AppendAudit(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListAudit(ctx, 10); err != nil || len(got) != 1 {
		t.Fatalf("audit=%+v err=%v", got, err)
	}
	if _, err := s.FirstAuditForResourceAfter(ctx, e.ResourceType, e.ResourceID, testNow.Add(time.Second), []string{e.Action}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("future audit lookup err=%v", err)
	}
	if _, err := s.FirstAuditForResourceAfter(ctx, e.ResourceType, e.ResourceID, testNow.Add(-time.Second), nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty action filter err=%v", err)
	}
	if got, err := s.FirstAuditForResourceAfter(ctx, e.ResourceType, e.ResourceID, testNow.Add(-time.Second), []string{"unrelated", e.Action}); err != nil || got.ID != e.ID || got.Action != e.Action {
		t.Fatalf("resource audit=%+v err=%v", got, err)
	}
	later := e
	later.ID = "audit-2"
	later.Action = "component_release.updated"
	later.CreatedAt = testNow.Add(time.Second)
	if err := s.AppendAudit(ctx, later); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FirstAuditForResourceAfter(ctx, e.ResourceType, e.ResourceID, testNow.Add(-time.Second), []string{later.Action}); err != nil || got.ID != later.ID {
		t.Fatalf("filtered resource audit=%+v err=%v", got, err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE audit_events SET action='tampered' WHERE id=?`, e.ID); err == nil {
		t.Fatal("append-only audit event was updated")
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM audit_events WHERE id=?`, e.ID); err == nil {
		t.Fatal("append-only audit event was deleted")
	}
}

func TestScenarioGraphEditInvalidatesTestAndReleasedIsImmutable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scenario := domain.Scenario{ID: "editable", Slug: "editable", Name: "Editable", OwnerID: "scenario-owner-a", CreatedAt: testNow, UpdatedAt: testNow}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: "release", Action: domain.ActionInstall, HostGroup: "all"}}, Edges: []domain.ScenarioEdge{}}
	revision := domain.ScenarioRevision{ID: "editable-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft, Graph: graph, CreatedAt: testNow}
	if err := s.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	graph.Nodes[0].Name = "edited"
	if err := s.SaveScenarioGraph(ctx, revision.ID, graph); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetScenarioRevision(ctx, revision.ID)
	if got.Status != domain.RevisionDraft || got.TestPassedAt != nil {
		t.Fatalf("edit did not invalidate test: %+v", got)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScenarioRevisionStatus(ctx, revision.ID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScenarioGraph(ctx, revision.ID, graph); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released graph edit=%v, want conflict", err)
	}
}
