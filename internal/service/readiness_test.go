package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func readinessTestPlatform(t *testing.T) (*Platform, *store.Store) {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seedPlatformOptionsForServiceTest(t, database)
	owner := domain.User{ID: "component-owner", Name: "Owner", Role: domain.RoleComponentOwner, CreatedAt: time.Now().UTC()}
	if err := database.UpsertUser(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	component := domain.Component{
		ID: "component-1", Slug: "component-1", Name: "Component", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"},
		OwnerID: owner.ID, CreatedAt: owner.CreatedAt, UpdatedAt: owner.CreatedAt,
	}
	if err := database.CreateComponent(context.Background(), component); err != nil {
		t.Fatal(err)
	}
	return NewPlatform(database, nil, nil), database
}

func readinessBlockerCodes(readiness domain.ReleaseReadiness) map[string]bool {
	codes := make(map[string]bool, len(readiness.Blockers))
	for _, blocker := range readiness.Blockers {
		codes[blocker.Code] = true
	}
	return codes
}

func TestReleaseReadinessExplainsLifecycleContractAndEvidenceBlockers(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	release := domain.ComponentRelease{
		ID: "release-1", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLevel("invalid"),
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, Actions: []domain.ActionDefinition{}, CreatedAt: time.Now().UTC(),
	}
	readiness, err := platform.releaseReadiness(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Status != domain.ReadinessBlocked || readiness.InstallEvidenceRunID != "" || readiness.RollbackEvidenceRunID != "" {
		t.Fatalf("readiness=%+v", readiness)
	}
	codes := readinessBlockerCodes(readiness)
	for _, code := range []string{"lifecycle_action_missing", "release_contract_invalid", "install_evidence_missing", "rollback_evidence_missing"} {
		if !codes[code] {
			t.Fatalf("missing blocker %q in %+v", code, readiness.Blockers)
		}
	}
	for _, blocker := range readiness.Blockers {
		if !strings.HasPrefix(blocker.ActionURL, "/components") {
			t.Fatalf("blocker lacks component action URL: %+v", blocker)
		}
	}
}

func TestComponentTestEvidenceAcceptsOnlyDeclaredRollbackSelfVerification(t *testing.T) {
	rollback := lockedStep{Action: domain.ActionRollback}
	if got := componentTestEvidence(domain.ActionRollback, []lockedStep{rollback}); got != "rollback_only" {
		t.Fatalf("unmarked rollback evidence=%q", got)
	}
	rollback.Tags = []string{rollbackSelfVerifyTag}
	if got := componentTestEvidence(domain.ActionRollback, []lockedStep{rollback}); got != "rollback_self_verify" {
		t.Fatalf("self-verifying rollback evidence=%q", got)
	}
	rollback.FromReleaseID = "release-new"
	rollback.ToReleaseID = "release-old"
	if got := componentTestEvidence(domain.ActionRollback, []lockedStep{rollback}); got != "rollback_only" {
		t.Fatalf("targeted self-verifying rollback evidence=%q", got)
	}
	verify := lockedStep{Action: domain.ActionVerify}
	if got := componentTestEvidence(domain.ActionRollback, []lockedStep{rollback, verify}); got != "rollback_verify" {
		t.Fatalf("explicit rollback verify evidence=%q", got)
	}
}

func TestClusterForgeMetadataTagsAreNotForwardedToAnsible(t *testing.T) {
	got := actionRuntimeTags([]string{"install", rollbackSelfVerifyTag, "network"})
	if len(got) != 2 || got[0] != "install" || got[1] != "network" {
		t.Fatalf("runtime tags=%v", got)
	}
}

func TestReleaseReadinessRequiresEvidenceForEveryRuntimeVersionPair(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	environmentOwner := domain.User{ID: "environment-owner", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: time.Now().UTC()}
	if err := database.UpsertUser(ctx, environmentOwner); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{
		ID: "release-runtime-matrix", ComponentID: "component-1", LineID: "line-runtime-matrix", LineName: "Runtime matrix",
		Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{"containerRuntime": []any{"docker"}, "containerRuntimeVersion": []any{"docker@20.10.21", "docker@24.0.9"}},
		Parameters:             []domain.ParameterDefinition{}, CreatedAt: time.Now().UTC(),
		Actions: []domain.ActionDefinition{
			{ID: "install", Kind: domain.ActionInstall, Playbook: "install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "verify", Kind: domain.ActionVerify, Playbook: "verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "rollback", Kind: domain.ActionRollback, Playbook: "rollback.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
		},
	}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, _ = database.GetComponentRelease(ctx, release.ID)
	digest := componentReleaseSpecDigest(release)
	environments := map[string]domain.Environment{}
	for _, version := range []string{"docker@20.10.21", "docker@24.0.9"} {
		facts := completeServiceTestFacts()
		facts["containerRuntimeVersion"] = version
		environment, err := platform.CreateEnvironment(ctx, environmentOwner, domain.Environment{Name: "Environment " + version}, facts)
		if err != nil {
			t.Fatal(err)
		}
		environments[version] = environment
	}
	record := func(version, evidence string, action domain.ActionKind) {
		environment := environments[version]
		now := time.Now().UTC()
		run := domain.Run{ID: "run-" + strings.ReplaceAll(version, "@", "-") + "-" + evidence, Kind: domain.RunComponentTest, Status: domain.RunSucceeded, RequestedBy: environmentOwner.ID, EnvironmentID: environment.ID, EnvironmentRevisionID: environment.CurrentRevisionID, ComponentReleaseID: release.ID, Action: action, InputSnapshot: map[string]any{"componentReleaseSpecDigest": digest, "componentTestEvidence": evidence, "runtimeCompatibility": domain.RuntimeCompatibility{Runtime: "docker", Version: version}}, CreatedAt: now, StartedAt: &now, FinishedAt: &now}
		if err := database.CreateRun(ctx, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	record("docker@20.10.21", "install_verify", domain.ActionInstall)
	record("docker@20.10.21", "rollback_verify", domain.ActionRollback)
	record("docker@24.0.9", "install_verify", domain.ActionInstall)
	evaluation := newReadinessEvaluation(platform)
	readiness, err := evaluation.readiness(ctx, release)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Status != domain.ReadinessBlocked || len(readiness.RuntimeEvidence) != 2 || !readiness.RuntimeEvidence[0].Complete || readiness.RuntimeEvidence[1].Complete {
		t.Fatalf("partial runtime readiness=%+v", readiness)
	}
	if !readinessBlockerCodes(readiness)["runtime_rollback_evidence_missing"] {
		t.Fatalf("missing runtime rollback blocker: %+v", readiness.Blockers)
	}
	record("docker@24.0.9", "rollback_verify", domain.ActionRollback)
	readiness, err = evaluation.readiness(ctx, release)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Status != domain.ReadinessReady || len(readiness.RuntimeEvidence) != 2 || !readiness.RuntimeEvidence[0].Complete || !readiness.RuntimeEvidence[1].Complete {
		t.Fatalf("complete runtime readiness=%+v", readiness)
	}
	// Reusing governance definitions must not reuse evidence for a changed
	// contract, even when the Release ID remains the same within this read.
	release.ReleaseNotes = "changed contract"
	readiness, err = evaluation.readiness(ctx, release)
	if err != nil || readiness.Status != domain.ReadinessBlocked || readiness.RuntimeEvidence[0].Complete || readiness.RuntimeEvidence[1].Complete {
		t.Fatalf("stale evidence reused for changed contract: %+v %v", readiness, err)
	}
}

func TestRuntimeConstraintRejectsMissingAndCrossParentVersions(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	ctx := context.Background()
	for name, constraints := range map[string]map[string]any{
		"missing version": {"containerRuntime": []any{"docker"}},
		"cross parent":    {"containerRuntime": []any{"docker"}, "containerRuntimeVersion": []any{"containerd@2.0.10"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := platform.validateEnvironmentConstraintsCatalog(ctx, constraints); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type digestFailureRunner struct{}

func (digestFailureRunner) Run(context.Context, ActionRequest) (ActionResult, error) {
	return ActionResult{}, nil
}

func (digestFailureRunner) Digest(string) (string, string, error) {
	return "", "", errors.New("playbook missing")
}

func TestValidateReleaseTransitionContractsLocksDirectionAndExecutablePlaybooks(t *testing.T) {
	platform, database := readinessTestPlatform(t)
	now := time.Now().UTC()
	previous := domain.ComponentRelease{
		ID: "release-old", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now, ReleasedAt: &now,
	}
	if err := database.CreateComponentRelease(context.Background(), previous); err != nil {
		t.Fatal(err)
	}
	previous, err := database.GetComponentRelease(context.Background(), previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := domain.ComponentRelease{
		ID: "release-new", ComponentID: "component-1", LineID: previous.LineID, ParentReleaseID: previous.ID,
		Compatibility: domain.CompatibilityCompatible, Version: "2.0.0", Status: domain.ReleaseDraft,
		Actions: []domain.ActionDefinition{
			{Name: "upgrade", Kind: domain.ActionUpgrade, Playbook: "upgrade.yml", FromReleaseID: previous.ID, ToReleaseID: "release-new"},
			{Name: "rollback", Kind: domain.ActionRollback, Playbook: "rollback.yml", FromReleaseID: "release-new", ToReleaseID: previous.ID},
		},
	}
	if err := platform.validateReleaseTransitionContracts(context.Background(), current); err != nil {
		t.Fatalf("valid transition contract=%v", err)
	}

	invalidUpgrade := current
	invalidUpgrade.Actions = append([]domain.ActionDefinition(nil), current.Actions...)
	invalidUpgrade.Actions[0].ToReleaseID = previous.ID
	if err := platform.validateReleaseTransitionContracts(context.Background(), invalidUpgrade); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("backward upgrade error=%v", err)
	}
	invalidRollback := current
	invalidRollback.Actions = append([]domain.ActionDefinition(nil), current.Actions...)
	invalidRollback.Actions[1].ToReleaseID = "missing"
	if err := platform.validateReleaseTransitionContracts(context.Background(), invalidRollback); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing rollback target error=%v", err)
	}

	platform.runner = digestFailureRunner{}
	if err := platform.validateReleaseTransitionContracts(context.Background(), current); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unexecutable playbook error=%v", err)
	}
}
