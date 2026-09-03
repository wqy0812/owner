package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

type countingPlaybookRunner struct {
	*ansible.Runner
	batches, singles int
	paths            []string
}

func (r *countingPlaybookRunner) ValidatePlaybooks(paths []string) map[string]error {
	r.batches++
	r.paths = append([]string(nil), paths...)
	return r.Runner.ValidatePlaybooks(paths)
}

func (r *countingPlaybookRunner) Digest(path string) (string, string, error) {
	r.singles++
	return r.Runner.Digest(path)
}

type sequentialPlaybookRunner struct {
	ActionRunner
	digestRunner
}

func TestReadinessBatchesFilesWithoutSharingMutationValidation(t *testing.T) {
	p, database := readinessTestPlatform(t)
	defer p.Close()
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fixtures"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install", "verify", "rollback"} {
		if err := os.WriteFile(filepath.Join(root, "fixtures", name+".yml"), []byte("- hosts: all\n  tasks: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &countingPlaybookRunner{Runner: &ansible.Runner{AllowedRoot: root}}
	p.runner = runner
	component, err := database.GetComponent(ctx, "component-1", false)
	if err != nil {
		t.Fatal(err)
	}
	component.ID, component.Slug = "component-2", "component-2"
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	valid := evaluationRelease("valid-release")
	invalid := evaluationRelease("invalid-release")
	invalid.ComponentID = component.ID
	invalid.Actions[0].Playbook = "fixtures/missing.yml"
	for _, release := range []domain.ComponentRelease{valid, invalid} {
		release.Compatibility = domain.CompatibilityNotApplicable
		if err := database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	owner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	batched, err := p.ListComponents(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if runner.batches != 1 || len(runner.paths) != 4 || runner.singles != 0 {
		t.Fatalf("batches=%d paths=%v single=%d", runner.batches, runner.paths, runner.singles)
	}
	for _, c := range batched {
		if got := readinessBlockerCodes(c.Releases[0].Readiness)["release_contract_invalid"]; got != (c.ID == invalid.ComponentID) {
			t.Fatalf("file failure attributed to wrong release: %+v", c.Releases[0].Readiness)
		}
	}
	work, err := p.Workbench(ctx, owner)
	if err != nil || len(work.Items) != 2 || runner.batches != 2 || runner.singles != 0 {
		t.Fatalf("workbench repeated validation: items=%d batches=%d single=%d err=%v", len(work.Items), runner.batches, runner.singles, err)
	}
	for _, item := range work.Items {
		if item.Status != domain.WorkStatusBlocked {
			t.Fatalf("workbench lost Readiness blockers: %+v", item)
		}
	}
	if _, _, err := componentOwnerWork(owner, []domain.Component{{OwnerID: owner.ID, Releases: []domain.ComponentRelease{valid}}}, nil); err == nil {
		t.Fatal("unevaluated Readiness was treated as publishable")
	}
	// Compare the entire response against the original per-action validation.
	p.runner = sequentialPlaybookRunner{ActionRunner: runner, digestRunner: runner}
	sequential, err := p.ListComponents(ctx, owner)
	if err != nil || !reflect.DeepEqual(batched, sequential) {
		t.Fatalf("batch changed the response: %v", err)
	}
	p.runner = runner
	evaluation := newReadinessEvaluation(p)
	evaluation.preparePlaybooks([]domain.Component{{Releases: []domain.ComponentRelease{valid}}})
	before := runner.singles
	if err := evaluation.validatePlaybook("unlisted.yml"); err == nil || runner.singles != before+1 {
		t.Fatal("unlisted dependency file bypassed live validation")
	}
	if err := os.Remove(filepath.Join(root, "fixtures", "install.yml")); err != nil {
		t.Fatal(err)
	}
	// A publication check must not inherit the earlier successful read check.
	if err := p.validateReleaseTransitionContracts(ctx, valid); err == nil {
		t.Fatal("mutation validation reused a successful file check")
	}
	next, err := p.GetComponent(ctx, owner, valid.ComponentID)
	if err != nil || !readinessBlockerCodes(next.Releases[0].Readiness)["release_contract_invalid"] {
		t.Fatalf("next detail read reused a deleted file: %v", err)
	}
}
