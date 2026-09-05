package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

type releaseCoordinatorStoreStub struct {
	releaseCoordinatorStore
	releases map[string]domain.ComponentRelease
	errors   map[string]error
}

func (s *releaseCoordinatorStoreStub) GetComponentRelease(_ context.Context, id string) (domain.ComponentRelease, error) {
	if err := s.errors[id]; err != nil {
		return domain.ComponentRelease{}, err
	}
	release, ok := s.releases[id]
	if !ok {
		return domain.ComponentRelease{}, domain.ErrNotFound
	}
	return release, nil
}

func TestCaptureReleasePublicationStateLocksDependencyAndTransitionClosure(t *testing.T) {
	releases := map[string]domain.ComponentRelease{
		"current": {
			ID: "current", ComponentID: "component", Version: "2.0.0", Status: domain.ReleaseDraft, Candidate: true, PublicationGeneration: 7,
			Dependencies: []domain.ComponentDependency{{UpstreamReleaseID: "dependency"}, {UpstreamReleaseID: "removed-dependency"}},
			Actions:      []domain.ActionDefinition{{Kind: domain.ActionUpgrade, FromReleaseID: "previous", ToReleaseID: "current"}},
		},
		"dependency": {ID: "dependency", ComponentID: "dependency-component", Version: "1.0.0", Status: domain.ReleaseReleased, PublicationGeneration: 2},
		"previous":   {ID: "previous", ComponentID: "component", Version: "1.0.0", Status: domain.ReleaseReleased, PublicationGeneration: 4},
	}
	coordinator := &ReleaseCoordinator{store: &releaseCoordinatorStoreStub{releases: releases}}
	captured, guards, err := coordinator.captureReleasePublicationState(context.Background(), []string{"current", "current"})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 3 {
		t.Fatalf("captured releases=%v", reflect.ValueOf(captured).MapKeys())
	}
	ids := make([]string, 0, len(guards))
	for _, guard := range guards {
		ids = append(ids, guard.ReleaseID)
		if guard.ReleaseID == "current" && (guard.PublicationGeneration != 7 || guard.SpecDigest != componentReleaseSpecDigest(releases["current"]) || !guard.Candidate) {
			t.Fatalf("current guard=%+v", guard)
		}
		if guard.ReleaseID == "removed-dependency" && !guard.Missing {
			t.Fatalf("removed dependency guard=%+v", guard)
		}
	}
	wantIDs := []string{"current", "dependency", "previous", "removed-dependency"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("guard ids=%v want=%v", ids, wantIDs)
	}
}

func TestCaptureReleasePublicationStateFailsForMissingRootOrEmptyReference(t *testing.T) {
	coordinator := &ReleaseCoordinator{store: &releaseCoordinatorStoreStub{releases: map[string]domain.ComponentRelease{}}}
	if _, _, err := coordinator.captureReleasePublicationState(context.Background(), []string{"missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing root error=%v", err)
	}
	if _, _, err := coordinator.captureReleasePublicationState(context.Background(), []string{""}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty root error=%v", err)
	}
}

func currentScenarioEvidence(revision domain.ScenarioRevision, releases ...domain.ComponentRelease) domain.Run {
	steps := make([]lockedStep, 0, len(releases))
	for index, release := range releases {
		steps = append(steps, lockedStep{ID: string(rune('a' + index)), ReleaseID: release.ID, ReleaseSpecDigest: componentReleaseSpecDigest(release)})
	}
	snapshot := structToMap(lockedPlan{Steps: steps})
	snapshot["scenarioRevisionSpecDigest"] = scenarioRevisionSpecDigest(revision)
	return domain.Run{
		ID: "scenario-test", Kind: domain.RunScenarioTest, Status: domain.RunSucceeded,
		ScenarioRevisionID: revision.ID, InputSnapshot: snapshot,
	}
}

func TestScenarioTestEvidenceRequiresCurrentRevisionAndReleaseDefinitions(t *testing.T) {
	release := domain.ComponentRelease{ID: "release-1", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft, Candidate: true}
	revision := domain.ScenarioRevision{
		ID: "revision-1", Status: domain.RevisionTestPassed,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node-1", ReleaseID: release.ID}}},
	}
	run := currentScenarioEvidence(revision, release)
	stub := &releaseCoordinatorStoreStub{releases: map[string]domain.ComponentRelease{release.ID: release}}
	coordinator := &ReleaseCoordinator{store: stub}
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || !current {
		t.Fatalf("current evidence=%v err=%v", current, err)
	}

	relocated := release
	relocated.Artifacts = []domain.ComponentArtifact{{Alias: "runtime", Filename: "runtime.tgz", SHA256: strings.Repeat("a", 64), SourceURL: "https://new-source.test/runtime.tgz"}}
	run = currentScenarioEvidence(revision, relocated)
	relocated.Artifacts[0].SourceURL = "https://another-source.test/runtime.tgz"
	stub.releases[release.ID] = relocated
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || !current {
		t.Fatalf("source-only repair invalidated evidence=%v err=%v", current, err)
	}

	changed := relocated
	changed.Version = "1.0.1"
	stub.releases[release.ID] = changed
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || current {
		t.Fatalf("changed definition evidence=%v err=%v", current, err)
	}

	stub.releases[release.ID] = relocated
	wrongRevision := revision
	wrongRevision.Graph.Nodes[0].ReleaseID = "release-2"
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, wrongRevision); err != nil || current {
		t.Fatalf("changed revision evidence=%v err=%v", current, err)
	}
	run.Status = domain.RunFailed
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || current {
		t.Fatalf("failed run evidence=%v err=%v", current, err)
	}
}

func TestReleasedScenarioMayRetainDeprecatedReleaseEvidence(t *testing.T) {
	release := domain.ComponentRelease{ID: "release-1", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseReleased}
	revision := domain.ScenarioRevision{
		ID: "revision-1", Status: domain.RevisionReleased,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node-1", ReleaseID: release.ID}}},
	}
	run := currentScenarioEvidence(revision, release)
	release.Status = domain.ReleaseDeprecated
	coordinator := &ReleaseCoordinator{store: &releaseCoordinatorStoreStub{releases: map[string]domain.ComponentRelease{release.ID: release}}}
	if current, err := coordinator.scenarioRunDefinitionCurrent(context.Background(), run, revision); err != nil || !current {
		t.Fatalf("released historical evidence=%v err=%v", current, err)
	}
	revision.Status = domain.RevisionDraft
	run = currentScenarioEvidence(revision, release)
	if current, err := coordinator.scenarioRunDefinitionCurrent(context.Background(), run, revision); err != nil || current {
		t.Fatalf("draft using deprecated release evidence=%v err=%v", current, err)
	}
}

func TestScenarioEvidenceRejectsConflictingDuplicateReleaseLocks(t *testing.T) {
	release := domain.ComponentRelease{ID: "release-1", Version: "1.0.0", Status: domain.ReleaseReleased}
	revision := domain.ScenarioRevision{ID: "revision-1", Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: release.ID}}}}
	snapshot := structToMap(lockedPlan{Steps: []lockedStep{
		{ID: "one", ReleaseID: release.ID, ReleaseSpecDigest: componentReleaseSpecDigest(release)},
		{ID: "two", ReleaseID: release.ID, ReleaseSpecDigest: "different"},
	}})
	snapshot["scenarioRevisionSpecDigest"] = scenarioRevisionSpecDigest(revision)
	run := domain.Run{ScenarioRevisionID: revision.ID, InputSnapshot: snapshot}
	coordinator := &ReleaseCoordinator{store: &releaseCoordinatorStoreStub{releases: map[string]domain.ComponentRelease{release.ID: release}}}
	if current, err := coordinator.scenarioRunDefinitionCurrent(context.Background(), run, revision); err != nil || current {
		t.Fatalf("conflicting locks current=%v err=%v", current, err)
	}
}

func TestReadinessErrorIncludesAllBlockers(t *testing.T) {
	if err := readinessError(domain.ReleaseReadiness{Status: domain.ReadinessReady}); err != nil {
		t.Fatalf("ready release error=%v", err)
	}
	err := readinessError(domain.ReleaseReadiness{Status: domain.ReadinessBlocked, Blockers: []domain.ReadinessBlocker{{Message: "missing install"}, {Message: "missing rollback"}}})
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "missing install; missing rollback") {
		t.Fatalf("blocked readiness error=%v", err)
	}
}

func TestMutualConfigurationCandidatesLockEverySourceAndRejectChangedEvidence(t *testing.T) {
	graph, releases, _ := configurationReferenceFixture()
	byID := map[string]domain.ComponentRelease{}
	for key, r := range releases {
		r.Status = domain.ReleaseDraft
		r.Candidate = true
		r.PublicationGeneration = 1
		releases[key] = r
		byID[r.ID] = r
	}
	stub := &releaseCoordinatorStoreStub{releases: byID}
	coordinator := &ReleaseCoordinator{store: stub}
	captured, guards, err := coordinator.captureReleasePublicationState(context.Background(), []string{"api-r1"})
	if err != nil || len(captured) != 2 || len(guards) != 2 {
		t.Fatalf("configuration closure locks: %v %v", guards, err)
	}
	revision := domain.ScenarioRevision{ID: "config-scenario", Status: domain.RevisionTestPassed, Graph: graph}
	run := currentScenarioEvidence(revision, releases["api"], releases["kubelet"])
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || !current {
		t.Fatalf("candidate evidence: %v %v", current, err)
	}
	changed := byID["kubelet-r1"]
	changed.Parameters = append([]domain.ParameterDefinition(nil), changed.Parameters...)
	changed.Parameters[0].SuggestedValue = "/changed"
	stub.releases[changed.ID] = changed
	if current, err := coordinator.scenarioTestEvidenceCurrent(context.Background(), run, revision); err != nil || current {
		t.Fatalf("changed source accepted: %v %v", current, err)
	}
}
