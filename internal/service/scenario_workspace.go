package service

import yaml "gopkg.in/yaml.v3"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"codex/platform-demo/internal/domain"
)

func scenarioAcceptancePrefix(revision domain.ScenarioRevision) string {
	return "managed-scenarios/" + revision.ScenarioID + "/" + revision.ID + "/"
}

func (p *ScenarioService) scenarioAcceptanceDirectory(revision domain.ScenarioRevision, create bool) (string, error) {
	if !scenarioAcceptanceIdentifier.MatchString(revision.ScenarioID) || !scenarioAcceptanceIdentifier.MatchString(revision.ID) {
		return "", fmt.Errorf("%w: invalid scenario workspace identity", domain.ErrInvalid)
	}
	if p.workspace.root == "" {
		return "", fmt.Errorf("%w: Playbook management is not configured", domain.ErrConflict)
	}
	root, err := filepath.Abs(p.workspace.root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	relative := strings.TrimSuffix(scenarioAcceptancePrefix(revision), "/")
	// Even read operations inspect every existing ancestor without following
	// symlinks. The component resolver alone only checked the final parent.
	current := root
	for _, segment := range strings.Split(relative, "/") {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if create {
				if err := os.Mkdir(current, 0750); err != nil && !os.IsExist(err) {
					return "", err
				}
				info, statErr = os.Lstat(current)
			} else {
				continue
			}
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%w: scenario workspace contains an unsafe directory", domain.ErrInvalid)
		}
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

func (p *ScenarioService) scenarioAcceptanceWorkspace(revision domain.ScenarioRevision) (PlaybookWorkspace, error) {
	directory, err := p.scenarioAcceptanceDirectory(revision, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	files, tree, err := scanWorkspace("", directory)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	return PlaybookWorkspace{Root: scenarioAcceptancePrefix(revision), TreeSHA256: tree, Files: files}, nil
}

func acceptanceFileJob(revision domain.ScenarioRevision, relative string) (*domain.ScenarioAcceptanceJob, bool) {
	for index := range revision.AcceptanceJobs {
		if relative == "tasks/acceptance/"+revision.AcceptanceJobs[index].ID+".yml" {
			return &revision.AcceptanceJobs[index], true
		}
	}
	return nil, false
}

func validateAcceptanceWorkspacePath(revision domain.ScenarioRevision, relative string) (string, error) {
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return "", err
	}
	if _, ok := acceptanceFileJob(revision, clean); ok {
		return clean, nil
	}
	if strings.HasPrefix(clean, "tasks/acceptance/") {
		return "", fmt.Errorf("%w: acceptance entrypoints are generated from job definitions", domain.ErrInvalid)
	}
	if !strings.HasPrefix(clean, "tasks/") && !strings.HasPrefix(clean, "templates/") && !strings.HasPrefix(clean, "files/") {
		return "", fmt.Errorf("%w: scenario workspace supports tasks, templates and files", domain.ErrInvalid)
	}
	if strings.HasPrefix(clean, "tasks/") && filepath.Ext(clean) != ".yml" && filepath.Ext(clean) != ".yaml" {
		return "", fmt.Errorf("%w: task files must use YAML", domain.ErrInvalid)
	}
	return clean, nil
}

func (p *ScenarioService) ListAcceptanceWorkspace(ctx context.Context, user domain.User, revisionID string) (PlaybookWorkspace, error) {
	revision, _, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	return p.scenarioAcceptanceWorkspace(revision)
}

func (p *ScenarioService) ReadAcceptanceFile(ctx context.Context, user domain.User, revisionID, relative string) (WorkspaceFile, []byte, error) {
	revision, _, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, false)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	directory, err := p.scenarioAcceptanceDirectory(revision, false)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return WorkspaceFile{}, nil, err
	}
	for _, file := range workspace.Files {
		if file.Path == clean {
			data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(clean)))
			if err != nil {
				return WorkspaceFile{}, nil, err
			}
			editable := len(data) <= MaxPlaybookBytes && utf8.Valid(data) && !strings.ContainsRune(string(data), 0)
			result := WorkspaceFile{ComponentPlaybookFile: file, Editable: editable}
			if editable {
				result.Content = string(data)
			}
			return result, data, nil
		}
	}
	return WorkspaceFile{}, nil, domain.ErrNotFound
}

type ScenarioWorkspaceExpectation struct {
	ConfirmYAMLMigration   bool    `json:"confirmYamlMigration,omitempty"`
	ExpectedRevisionDigest string  `json:"expectedRevisionDigest"`
	ExpectedSHA256         *string `json:"expectedSha256"`
	ExpectedTreeSHA256     *string `json:"expectedTreeSha256"`
}

func checkScenarioWorkspaceExpectation(revision domain.ScenarioRevision, workspace PlaybookWorkspace, relative string, expected ScenarioWorkspaceExpectation) error {
	if expected.ExpectedRevisionDigest == "" || expected.ExpectedSHA256 == nil || expected.ExpectedTreeSHA256 == nil {
		return fmt.Errorf("%w: revision, file and workspace expectations are required", domain.ErrInvalid)
	}
	if expected.ExpectedRevisionDigest != domain.ScenarioRevisionSpecDigest(revision) || !strings.EqualFold(*expected.ExpectedTreeSHA256, workspace.TreeSHA256) {
		return fmt.Errorf("%w: scenario revision or workspace changed; reload before saving", domain.ErrConflict)
	}
	actual := ""
	for _, file := range workspace.Files {
		if file.Path == relative {
			actual = file.SHA256
		}
	}
	if !strings.EqualFold(actual, *expected.ExpectedSHA256) {
		return fmt.Errorf("%w: acceptance file changed since it was loaded", domain.ErrConflict)
	}
	return nil
}

func (p *ScenarioService) SaveAcceptanceFile(ctx context.Context, user domain.User, revisionID, relative string, contents []byte, expected ScenarioWorkspaceExpectation) (WorkspaceFile, error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	revision, _, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, true)
	if err != nil {
		return WorkspaceFile{}, err
	}
	clean, err := validateAcceptanceWorkspacePath(revision, relative)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if len(contents) > MaxWorkspaceFileBytes {
		return WorkspaceFile{}, fmt.Errorf("%w: workspace file exceeds 10 MiB", domain.ErrInvalid)
	}
	if strings.HasPrefix(clean, "tasks/") {
		mayMutate := true
		if job, ok := acceptanceFileJob(revision, clean); ok {
			mayMutate = job.MayMutate
		}
		if err := validateScenarioAcceptanceTasks(contents, mayMutate); err != nil {
			return WorkspaceFile{}, err
		}
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if err := checkScenarioWorkspaceExpectation(revision, workspace, clean, expected); err != nil {
		return WorkspaceFile{}, err
	}
	directory, err := p.scenarioAcceptanceDirectory(revision, true)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if err := ensureRealDirectories(directory, filepath.ToSlash(filepath.Dir(clean))); err != nil {
		return WorkspaceFile{}, err
	}
	target := filepath.Join(directory, filepath.FromSlash(clean))
	previous, readErr := os.ReadFile(target)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return WorkspaceFile{}, readErr
	}
	if err := replaceWorkspaceFileAtomically(target, contents); err != nil {
		return WorkspaceFile{}, err
	}
	if expected.ConfirmYAMLMigration {
		job, ok := acceptanceFileJob(revision, clean)
		if !ok {
			_ = restoreWorkspaceFile(target, existed, previous)
			return WorkspaceFile{}, fmt.Errorf("%w: 只能在验收入口 YAML 保存时确认迁入", domain.ErrInvalid)
		}
		for i := range revision.AcceptanceJobs {
			if revision.AcceptanceJobs[i].ID == job.ID {
				revision.AcceptanceJobs[i].GatherFacts = false
				revision.AcceptanceJobs[i].RuntimeChecks = nil
			}
		}
	}
	if err := p.persistScenarioAcceptanceWorkspace(ctx, revision, expected.ExpectedRevisionDigest); err != nil {
		if restoreErr := restoreWorkspaceFile(target, existed, previous); restoreErr != nil {
			return WorkspaceFile{}, fmt.Errorf("save acceptance workspace: %w; restore file: %v", err, restoreErr)
		}
		return WorkspaceFile{}, err
	}
	p.audit.Record(ctx, user, "scenario_acceptance_file.saved", "scenario_revision", revisionID, map[string]any{"path": clean, "bytes": len(contents)})
	file, _, err := p.ReadAcceptanceFile(ctx, user, revisionID, clean)
	return file, err
}

func (p *ScenarioService) DeleteAcceptanceFile(ctx context.Context, user domain.User, revisionID, relative string, expected ScenarioWorkspaceExpectation) (PlaybookWorkspace, error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	revision, _, err := p.authorizeScenarioAcceptance(ctx, user, revisionID, true)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	clean, err := cleanWorkspaceRelative(relative)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if err := checkScenarioWorkspaceExpectation(revision, workspace, clean, expected); err != nil {
		return PlaybookWorkspace{}, err
	}
	if *expected.ExpectedSHA256 == "" {
		return PlaybookWorkspace{}, domain.ErrNotFound
	}
	directory, err := p.scenarioAcceptanceDirectory(revision, false)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	target := filepath.Join(directory, filepath.FromSlash(clean))
	previous, err := os.ReadFile(target)
	if err != nil {
		return PlaybookWorkspace{}, err
	}
	if err := removeWorkspaceFileDurably(target); err != nil {
		return PlaybookWorkspace{}, err
	}
	if err := p.persistScenarioAcceptanceWorkspace(ctx, revision, expected.ExpectedRevisionDigest); err != nil {
		if restoreErr := restoreWorkspaceFile(target, true, previous); restoreErr != nil {
			return PlaybookWorkspace{}, fmt.Errorf("delete acceptance source: %w; restore: %v", err, restoreErr)
		}
		return PlaybookWorkspace{}, err
	}
	p.audit.Record(ctx, user, "scenario_acceptance_file.deleted", "scenario_revision", revisionID, map[string]any{"path": clean})
	return p.ListAcceptanceWorkspace(ctx, user, revisionID)
}

func (p *ScenarioService) persistScenarioAcceptanceWorkspace(ctx context.Context, revision domain.ScenarioRevision, expectedDigest string) error {
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return err
	}
	revision.AcceptanceWorkspaceRoot, revision.AcceptanceTreeSHA256 = workspace.Root, workspace.TreeSHA256
	for index := range revision.AcceptanceJobs {
		job := &revision.AcceptanceJobs[index]
		job.PlaybookSHA256 = ""
		for _, file := range workspace.Files {
			if workspace.Root+file.Path == job.Playbook {
				job.PlaybookSHA256 = file.SHA256
			}
		}
	}
	return p.store.SaveScenarioRevisionDefinition(ctx, revision, expectedDigest)
}

// CloneScenarioAcceptanceWorkspace rebases every immutable source reference and
// copies bytes to an independent revision directory. The caller commits its
// scenario/revision transaction afterwards and invokes cleanup only on failure.
func (p *ScenarioService) CloneScenarioAcceptanceWorkspace(ctx context.Context, source domain.ScenarioRevision, target *domain.ScenarioRevision) (func(), error) {
	p.workspace.mu.Lock()
	defer p.workspace.mu.Unlock()
	noop := func() {}
	if err := ctx.Err(); err != nil {
		return noop, err
	}
	if source.ID == target.ID && source.ScenarioID == target.ScenarioID {
		return noop, fmt.Errorf("%w: clone target must be a new revision", domain.ErrInvalid)
	}
	if len(source.AcceptanceJobs) == 0 && source.AcceptanceTreeSHA256 == "" {
		target.AcceptanceWorkspaceRoot = scenarioAcceptancePrefix(*target)
		target.AcceptanceTreeSHA256 = ""
		return noop, nil
	}
	workspace, err := p.scenarioAcceptanceWorkspace(source)
	if err != nil {
		return noop, err
	}
	if workspace.TreeSHA256 != source.AcceptanceTreeSHA256 && (len(workspace.Files) > 0 || source.AcceptanceTreeSHA256 != "") {
		return noop, fmt.Errorf("%w: source acceptance workspace differs from its revision manifest", domain.ErrConflict)
	}
	from, err := p.scenarioAcceptanceDirectory(source, false)
	if err != nil {
		return noop, err
	}
	to, err := p.scenarioAcceptanceDirectory(*target, false)
	if err != nil {
		return noop, err
	}
	if _, err := os.Lstat(to); err == nil {
		return noop, fmt.Errorf("%w: target acceptance workspace already exists", domain.ErrConflict)
	} else if !os.IsNotExist(err) {
		return noop, err
	}
	target.AcceptanceJobs = append([]domain.ScenarioAcceptanceJob(nil), source.AcceptanceJobs...)
	target.AcceptanceWorkspaceRoot, target.AcceptanceTreeSHA256 = scenarioAcceptancePrefix(*target), workspace.TreeSHA256
	for index := range target.AcceptanceJobs {
		target.AcceptanceJobs[index].Playbook = scenarioAcceptancePrefix(*target) + "tasks/acceptance/" + target.AcceptanceJobs[index].ID + ".yml"
	}
	if len(workspace.Files) == 0 {
		return noop, nil
	}
	to, err = p.scenarioAcceptanceDirectory(*target, true)
	if err != nil {
		return noop, err
	}
	cleanup := func() { _ = os.RemoveAll(to) }
	for _, file := range workspace.Files {
		if err := ctx.Err(); err != nil {
			cleanup()
			return noop, err
		}
		if err := ensureRealDirectories(to, filepath.ToSlash(filepath.Dir(file.Path))); err != nil {
			cleanup()
			return noop, err
		}
		data, err := os.ReadFile(filepath.Join(from, filepath.FromSlash(file.Path)))
		if err != nil {
			cleanup()
			return noop, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != file.SHA256 {
			cleanup()
			return noop, fmt.Errorf("%w: source acceptance file changed during copy", domain.ErrConflict)
		}
		if err := replaceWorkspaceFileAtomically(filepath.Join(to, filepath.FromSlash(file.Path)), data); err != nil {
			cleanup()
			return noop, err
		}
	}
	return cleanup, nil
}

func (p *ScenarioService) validateScenarioAcceptanceWorkspace(revision domain.ScenarioRevision) error {
	if revision.AcceptanceWorkspaceRoot != scenarioAcceptancePrefix(revision) || revision.AcceptanceTreeSHA256 == "" {
		return fmt.Errorf("%w: scenario acceptance has no persisted workspace identity", domain.ErrConflict)
	}
	workspace, err := p.scenarioAcceptanceWorkspace(revision)
	if err != nil {
		return err
	}
	if workspace.TreeSHA256 != revision.AcceptanceTreeSHA256 {
		return fmt.Errorf("%w: scenario acceptance workspace changed outside the revision", domain.ErrConflict)
	}
	directory, err := p.scenarioAcceptanceDirectory(revision, false)
	if err != nil {
		return err
	}
	for _, job := range revision.AcceptanceJobs {
		if job.NeedsYAMLMigration() {
			return domain.YAMLMigrationRequired(job.Name)
		}
		relative := "tasks/acceptance/" + job.ID + ".yml"
		if job.Playbook != workspace.Root+relative || job.PlaybookSHA256 == "" {
			return fmt.Errorf("%w: acceptance job %s has no valid source", domain.ErrInvalid, job.Name)
		}
		contents, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil {
			return fmt.Errorf("%w: acceptance source %s is missing", domain.ErrInvalid, job.Name)
		}
		sum := sha256.Sum256(contents)
		if hex.EncodeToString(sum[:]) != job.PlaybookSHA256 {
			return fmt.Errorf("%w: acceptance source differs from persisted job digest", domain.ErrConflict)
		}
		if err := validateScenarioAcceptanceTasks(contents, job.MayMutate); err != nil {
			return err
		}
		if err := validateScenarioAcceptanceIncludes(directory, relative, job.MayMutate, map[string]bool{}); err != nil {
			return err
		}
	}
	// All included tasks obey acceptance failure semantics as well. The runner
	// additionally validates the include graph and its containment at bundling.
	for _, file := range workspace.Files {
		if strings.HasPrefix(file.Path, "tasks/") {
			contents, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file.Path)))
			if err != nil {
				return err
			}
			if err := validateScenarioAcceptanceTasks(contents, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *WorkspaceVerifier) verifyScenarioAcceptanceStep(ctx context.Context, step *lockedStep) error {
	if step.SourceType != "scenario_acceptance" || step.ScenarioRevisionID == "" || step.AcceptanceJobID == "" || step.ReleaseID != "" {
		return fmt.Errorf("%w: invalid scenario acceptance step identity", domain.ErrConflict)
	}
	revision, err := p.store.GetScenarioRevision(ctx, step.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if err := p.scenarios.validateScenarioAcceptanceWorkspace(revision); err != nil {
		return err
	}
	for _, job := range revision.AcceptanceJobs {
		if job.ID == step.AcceptanceJobID {
			if step.Playbook != job.Playbook || step.Action != "acceptance" || step.ActionID != job.ID || step.Limit != job.HostGroup || step.TimeoutSeconds != job.TimeoutSeconds || step.Become != job.Become || step.GatherFacts != job.GatherFacts || step.MayMutate != job.MayMutate || !reflect.DeepEqual(step.RequiredCredentials, job.RequiredCredentials) || step.NeedsApproval != (job.RiskLevel == domain.RiskHigh || job.RiskLevel == domain.RiskDestructive) {
				return fmt.Errorf("%w: scenario acceptance job changed after planning", domain.ErrConflict)
			}
			digester := p.inspector
			sourceDigest, treeDigest, err := digester.Digest(job.Playbook)
			if err != nil {
				return err
			}
			if sourceDigest != job.PlaybookSHA256 {
				return fmt.Errorf("%w: scenario acceptance bytes differ from saved source", domain.ErrConflict)
			}
			if step.PlaybookDigest != "" && step.PlaybookDigest != sourceDigest {
				return fmt.Errorf("%w: scenario acceptance source changed after planning", domain.ErrConflict)
			}
			step.PlaybookDigest, step.WorkspaceDigest = sourceDigest, treeDigest
			return nil
		}
	}
	return fmt.Errorf("%w: scenario acceptance job no longer exists", domain.ErrConflict)
}

// Each include inherits the entrypoint's declared mutation policy. Validating
// auxiliary task files only in isolation would let a read-only job include a
// mutating helper. Fixed paths are enforced by ValidateRoleTasks beforehand.
func validateScenarioAcceptanceIncludes(directory, relative string, mayMutate bool, visiting map[string]bool) error {
	if visiting[relative] {
		return fmt.Errorf("%w: recursive acceptance task include", domain.ErrInvalid)
	}
	visiting[relative] = true
	defer delete(visiting, relative)
	contents, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
	if err != nil {
		return fmt.Errorf("%w: acceptance included tasks are missing", domain.ErrInvalid)
	}
	if err := validateScenarioAcceptanceTasks(contents, mayMutate); err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	var tasks func(*yaml.Node) error
	tasks = func(node *yaml.Node) error {
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				if err := tasks(child); err != nil {
					return err
				}
			}
			return nil
		}
		if node.Kind != yaml.MappingNode {
			return nil
		}
		for index := 0; index < len(node.Content); index += 2 {
			key, value := strings.TrimPrefix(node.Content[index].Value, "ansible.builtin."), node.Content[index+1]
			if key == "block" {
				if err := tasks(value); err != nil {
					return err
				}
			}
			if key != "include_tasks" && key != "import_tasks" {
				continue
			}
			name := value.Value
			if value.Kind == yaml.MappingNode {
				for index := 0; index < len(value.Content); index += 2 {
					if value.Content[index].Value == "file" {
						name = value.Content[index+1].Value
					}
				}
			}
			candidate := filepath.ToSlash(filepath.Join(filepath.Dir(relative), name))
			if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(candidate))); os.IsNotExist(err) {
				candidate = "tasks/" + name
			}
			clean, err := cleanWorkspaceRelative(candidate)
			if err != nil || !strings.HasPrefix(clean, "tasks/") {
				return fmt.Errorf("%w: acceptance include escapes tasks", domain.ErrInvalid)
			}
			if err := validateScenarioAcceptanceIncludes(directory, clean, mayMutate, visiting); err != nil {
				return err
			}
		}
		return nil
	}
	return tasks(document.Content[0])
}
