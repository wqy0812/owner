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
	current := domain.ComponentRelease{
		ID: "release-new", ComponentID: "component-1", Version: "2.0.0", Status: domain.ReleaseDraft,
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
