package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func evaluationRelease(id string) domain.ComponentRelease {
	r := domain.ComponentRelease{ID: id, ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow}
	for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionVerify, domain.ActionRollback} {
		r.Actions = append(r.Actions, domain.ActionDefinition{ID: id + "-" + string(kind), ReleaseID: id, Kind: kind, Name: string(kind), Playbook: "fixtures/" + string(kind) + ".yml", HostGroup: "test_nodes", TimeoutSeconds: 60})
	}
	return r
}

func TestReadinessEvaluationReusesDefinitionsOnlyWithinOneRead(t *testing.T) {
	p, database := readinessTestPlatform(t)
	defer p.Close()
	ctx := context.Background()
	evaluation := newReadinessEvaluation(p)
	reads := 0
	evaluation.readCatalog = func(ctx context.Context) (store.CatalogValidationSnapshot, error) {
		reads++
		return database.ReadCatalogDefinitions(ctx)
	}
	for _, id := range []string{"first", "second", "third"} {
		r := evaluationRelease(id)
		actual, err := evaluation.readiness(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := p.releaseReadiness(ctx, r)
		if err != nil || !reflect.DeepEqual(actual, expected) || readinessBlockerCodes(actual)["release_contract_invalid"] {
			t.Fatalf("readiness changed: actual=%+v expected=%+v err=%v", actual, expected, err)
		}
	}
	if reads != 1 {
		t.Fatalf("read catalog %d times, want 1", reads)
	}
	// An option added after the snapshot belongs to the next read operation.
	if err := database.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{ID: "new-group", Value: "new_group", Label: "New group", CreatedBy: "component-owner"}); err != nil {
		t.Fatal(err)
	}
	r := evaluationRelease("new-group-release")
	r.Actions[0].HostGroup = "new_group"
	oldSnapshot, err := evaluation.readiness(ctx, r)
	if err != nil || !readinessBlockerCodes(oldSnapshot)["release_contract_invalid"] {
		t.Fatalf("old snapshot unexpectedly changed: %+v %v", oldSnapshot, err)
	}
	nextSnapshot, err := p.releaseReadiness(ctx, r)
	if err != nil || readinessBlockerCodes(nextSnapshot)["release_contract_invalid"] {
		t.Fatalf("new read reused stale definitions: %+v %v", nextSnapshot, err)
	}
	// Mutation validation always loads current definitions independently.
	if err := p.validateReleaseContract(ctx, r, false); err != nil {
		t.Fatalf("write validation reused a read snapshot: %v", err)
	}
}

func TestReadinessDefinitionFailureRemainsBlocked(t *testing.T) {
	p, _ := readinessTestPlatform(t)
	defer p.Close()
	evaluation := newReadinessEvaluation(p)
	reads := 0
	evaluation.readCatalog = func(context.Context) (store.CatalogValidationSnapshot, error) {
		reads++
		return store.CatalogValidationSnapshot{}, errors.New("dictionary unavailable")
	}
	for _, id := range []string{"first", "second"} {
		readiness, err := evaluation.readiness(context.Background(), evaluationRelease(id))
		codes := readinessBlockerCodes(readiness)
		if err != nil || readiness.Status != domain.ReadinessBlocked || !codes["release_contract_invalid"] {
			t.Fatalf("definition failure did not block: %+v %v", readiness, err)
		}
	}
	if reads != 1 {
		t.Fatalf("failed dictionary was retried %d times within one read", reads)
	}
}
