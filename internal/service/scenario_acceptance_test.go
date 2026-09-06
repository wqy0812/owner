package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func scenarioAcceptanceFixture(t *testing.T) (*Platform, *store.Store, domain.User, domain.ScenarioRevision, string) {
	t.Helper()
	p, db, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "acceptance-owner", Name: "Scenario owner", Role: domain.RoleScenarioOwner, CreatedAt: time.Now().UTC()}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePlatformOptionCategory(ctx, domain.PlatformOptionCategory{ID: "acceptance-hosts", Key: "hostGroup", Label: "Host groups", Kind: domain.PlatformOptionHostGroup, CreatedBy: owner.ID, CreatedAt: time.Now().UTC()}, newAuditEvent(owner, "test", "category", "acceptance-hosts", nil)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{ID: "acceptance-host-group", Value: "all", Label: "All nodes", CreatedBy: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	scenario, err := p.scenarios.Create(ctx, owner, domain.Scenario{Name: "Business", Slug: "business"})
	if err != nil {
		t.Fatal(err)
	}
	revision := scenario.Revisions[0]
	definition, err := p.scenarios.SaveAcceptance(ctx, owner, revision.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: []domain.ScenarioAcceptanceJob{{ID: "business", Name: "Business health", Purpose: "Verify the complete service", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = definition
	revision, err = db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p, db, owner, revision, root
}

func acceptanceExpectation(t *testing.T, p *Platform, user domain.User, id, path string) ScenarioWorkspaceExpectation {
	t.Helper()
	definition, err := p.scenarios.ReadAcceptance(context.Background(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	sha := ""
	for _, file := range definition.Workspace.Files {
		if file.Path == path {
			sha = file.SHA256
		}
	}
	tree := definition.Workspace.TreeSHA256
	return ScenarioWorkspaceExpectation{ExpectedRevisionDigest: definition.RevisionDigest, ExpectedSHA256: &sha, ExpectedTreeSHA256: &tree}
}

func TestScenarioAcceptanceSourceCASAndEvidenceInvalidation(t *testing.T) {
	p, db, owner, revision, _ := scenarioAcceptanceFixture(t)
	ctx := context.Background()
	path := "tasks/acceptance/business.yml"
	expected := acceptanceExpectation(t, p, owner, revision.ID, path)
	file, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("- ansible.builtin.assert:\n    that: true\n"), expected)
	if err != nil {
		t.Fatal(err)
	}
	if file.SHA256 == "" || !file.Editable {
		t.Fatalf("file=%+v", file)
	}
	if _, err = p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("- ansible.builtin.assert:\n    that: false\n"), expected); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale source accepted: %v", err)
	}
	before, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.scenarios.validateScenarioAcceptanceWorkspace(before); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().ExecContext(ctx, `UPDATE scenario_revisions SET status='test_passed',test_passed_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), revision.ID); err != nil {
		t.Fatal(err)
	}
	path = "templates/query.j2"
	if _, err = p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("{{ cf.inputs.query }}\n"), acceptanceExpectation(t, p, owner, revision.ID, path)); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.RevisionDraft || after.TestPassedAt != nil || domain.ScenarioRevisionSpecDigest(after) == domain.ScenarioRevisionSpecDigest(before) {
		t.Fatalf("source mutation did not invalidate evidence: %+v", after)
	}
	other := owner
	other.ID = "someone-else"
	if _, err = p.scenarios.SaveAcceptanceFile(ctx, other, revision.ID, path, []byte("x"), acceptanceExpectation(t, p, owner, revision.ID, path)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-owner could mutate source: %v", err)
	}
	if _, err = db.DB().ExecContext(ctx, `UPDATE scenario_revisions SET status='released' WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("x"), acceptanceExpectation(t, p, owner, revision.ID, path)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("published source changed: %v", err)
	}
}

func TestScenarioAcceptanceWorkspaceRejectsEscapesAndOutOfBandSource(t *testing.T) {
	p, db, owner, revision, root := scenarioAcceptanceFixture(t)
	ctx := context.Background()
	path := "tasks/acceptance/business.yml"
	if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("- ansible.builtin.assert:\n    that: true\n"), acceptanceExpectation(t, p, owner, revision.ID, path)); err != nil {
		t.Fatal(err)
	}
	revision, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../escape.yml", "tasks/acceptance/unregistered.yml", "roles/helper/tasks/main.yml", "tasks/script.py"} {
		if _, err = p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, bad, []byte("x"), acceptanceExpectation(t, p, owner, revision.ID, bad)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("unsafe path %s accepted: %v", bad, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(revision.AcceptanceJobs[0].Playbook)), []byte("- ansible.builtin.assert:\n    that: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.scenarios.validateScenarioAcceptanceWorkspace(revision); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("out-of-band source accepted: %v", err)
	}
	directory, err := p.scenarios.scenarioAcceptanceDirectory(revision, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(directory, "files")); err != nil {
		t.Fatal(err)
	}
	if _, err = p.scenarios.ListAcceptanceWorkspace(ctx, owner, revision.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("workspace symlink accepted: %v", err)
	}
}

func TestScenarioAcceptanceCloneCopiesIndependentFilesAndRebasesIdentity(t *testing.T) {
	p, db, owner, source, root := scenarioAcceptanceFixture(t)
	ctx := context.Background()
	for path, content := range map[string]string{"tasks/acceptance/business.yml": "- ansible.builtin.assert:\n    that: true\n", "files/request.txt": "request"} {
		if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, source.ID, path, []byte(content), acceptanceExpectation(t, p, owner, source.ID, path)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := db.GetScenarioRevision(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := source
	target.ID = "scenario-revision-new"
	target.ScenarioID = "scenario-new"
	cleanup, err := p.scenarios.CloneScenarioAcceptanceWorkspace(ctx, source, &target)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if target.AcceptanceTreeSHA256 != source.AcceptanceTreeSHA256 || target.AcceptanceWorkspaceRoot == source.AcceptanceWorkspaceRoot || target.AcceptanceJobs[0].Playbook == source.AcceptanceJobs[0].Playbook {
		t.Fatalf("clone did not rebase independent workspace: %+v", target)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(target.AcceptanceJobs[0].Playbook)), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.scenarios.validateScenarioAcceptanceWorkspace(source); err != nil {
		t.Fatalf("target mutation changed source: %v", err)
	}
	if _, err := p.scenarios.CloneScenarioAcceptanceWorkspace(ctx, source, &target); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("clone overwrote existing workspace: %v", err)
	}
}

func TestScenarioAcceptanceTasksPreserveFailureAndMutationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		mutate, bad  bool
	}{
		{"inspect", "- ansible.builtin.command: healthcheck\n  changed_when: false\n", false, false},
		{"temporary operation", "- ansible.builtin.copy:\n    dest: /tmp/probe\n    content: probe\n", true, false},
		{"unadvertised operation", "- ansible.builtin.copy:\n    dest: /tmp/probe\n    content: probe\n", false, true},
		{"ignored failure", "- command: false\n  ignore_errors: true\n", true, true},
		{"suppressed failure", "- command: false\n  failed_when: false\n", true, true},
		{"quoted suppressed failure", "- command: false\n  failed_when: 'false'\n", true, true},
		{"suppressed list failure", "- command: false\n  failed_when: [result.rc != 0, false]\n", true, true},
		{"rescue", "- block:\n    - command: false\n  rescue:\n    - debug: msg=ignored\n", true, true},
		{"cleanup after failure", "- block:\n    - command: false\n  always:\n    - file: path=/tmp/probe state=absent\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateScenarioAcceptanceTasks([]byte(tc.source), tc.mutate); (err != nil) != tc.bad {
				t.Fatalf("err=%v wantBad=%v", err, tc.bad)
			}
		})
	}
}

func TestScenarioAcceptanceIncludedTasksInheritReadOnlyPolicy(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "tasks", "acceptance"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "tasks", "acceptance", "check.yml"), []byte("- ansible.builtin.include_tasks: helper.yml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "tasks", "helper.yml"), []byte("- ansible.builtin.copy:\n    dest: /tmp/mutation\n    content: value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateScenarioAcceptanceIncludes(directory, "tasks/acceptance/check.yml", false, map[string]bool{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("read-only source included mutation: %v", err)
	}
	if err := validateScenarioAcceptanceIncludes(directory, "tasks/acceptance/check.yml", true, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
}

func TestScenarioAcceptanceParametersUseResolvedValuesAndTypedValidation(t *testing.T) {
	revision := domain.ScenarioRevision{AcceptanceParameters: []domain.ParameterDefinition{{Name: "endpoint", Type: domain.ParameterTypeString, Required: true}, {Name: "retries", Type: domain.ParameterTypeInteger, Required: true}}, AcceptanceBindings: []domain.ScenarioParameterBinding{{Parameter: "endpoint", Source: "node", NodeID: "gateway", SourceParameter: "public_endpoint"}, {Parameter: "retries", Source: "environment", SourceParameter: "retries"}}}
	environment := domain.EnvironmentRevision{Parameters: map[string]any{"retries": 3}}
	values, err := resolveAcceptanceParameters(revision, environment, map[string]map[string]any{"gateway": {"public_endpoint": "mapped-endpoint"}})
	if err != nil || values["endpoint"] != "mapped-endpoint" || values["retries"] != 3 {
		t.Fatalf("resolved=%v err=%v", values, err)
	}
	environment.Parameters["retries"] = "bad"
	if _, err = resolveAcceptanceParameters(revision, environment, map[string]map[string]any{"gateway": {"public_endpoint": "mapped-endpoint"}}); err == nil {
		t.Fatal("mistyped environment value accepted")
	}
}

func TestScenarioAcceptanceLockedStepsHaveIndependentIdentityAndDetectSourceDrift(t *testing.T) {
	p, db, owner, revision, root := scenarioAcceptanceFixture(t)
	ctx := context.Background()
	path := "tasks/acceptance/business.yml"
	if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, path, []byte("- ansible.builtin.assert:\n    that: true\n"), acceptanceExpectation(t, p, owner, revision.ID, path)); err != nil {
		t.Fatal(err)
	}
	revision, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	acceptanceRunner, err := ansiblerunner.NewRunner(root)
	setTestRunner(t, p, acceptanceRunner)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := p.planner.lockScenarioAcceptanceSteps(ctx, revision, domain.EnvironmentRevision{}, map[string]map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].NodeID != "acceptance:business" || steps[0].SourceType != "scenario_acceptance" || steps[0].ReleaseID != "" || steps[0].Stage != "acceptance" || !steps[0].RetrySafe {
		t.Fatalf("wrong acceptance lock: %+v", steps)
	}
	if err = p.workspaceVerifier.verifyScenarioAcceptanceStep(ctx, &steps[0]); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(revision.AcceptanceJobs[0].Playbook)), []byte("- debug: msg=changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.workspaceVerifier.verifyScenarioAcceptanceStep(ctx, &steps[0]); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("source drift accepted: %v", err)
	}
}
