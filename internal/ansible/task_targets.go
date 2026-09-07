package ansible

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TaskTargetScope is sealed into reset jobs. It permits only inventory host
// names, so an IP alias or a fresh DNS lookup cannot expand the approved scope.
type TaskTargetScope struct {
	Hosts  []string            `json:"hosts"`
	Groups map[string][]string `json:"groups"`
}

var inventorySelfTarget = regexp.MustCompile(`^\{\{\s*inventory_hostname\s*\}\}$`)
var inventoryGroupTarget = regexp.MustCompile(`^\{\{\s*groups\[['"]([a-zA-Z0-9_.-]+)['"]\]\s*\[\s*([0-9]+)\s*\]\s*\}\}$`)

func validateTaskTargetAttributes(task *yaml.Node, scope *TaskTargetScope) error {
	if scope == nil || task.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(task.Content); i += 2 {
		key, value := task.Content[i].Value, task.Content[i+1]
		switch key {
		case "connection", "local_action":
			return fmt.Errorf("reset tasks cannot override their target connection with %s", key)
		case "delegate_to":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("reset delegate_to must identify a locked inventory host")
			}
			target := strings.TrimSpace(value.Value)
			if inventorySelfTarget.MatchString(target) {
				continue
			}
			candidates := []string{target}
			if group := inventoryGroupTarget.FindStringSubmatch(target); group != nil {
				index, err := strconv.Atoi(group[2])
				hosts := scope.Groups[group[1]]
				if err != nil || index >= len(hosts) {
					return fmt.Errorf("reset delegate_to group selector has no locked host: %s", target)
				}
				// Check the whole group, independently of Ansible inventory ordering.
				candidates = hosts
			}
			for _, candidate := range candidates {
				found := false
				for _, host := range scope.Hosts {
					found = found || candidate == host
				}
				if !found {
					return fmt.Errorf("reset delegate_to %q is outside the locked reset hosts or cannot be resolved statically", target)
				}
			}
		}
	}
	return nil
}

func validateRoleTaskTargets(root, entry string, scope *TaskTargetScope) error {
	if scope == nil {
		return nil
	}
	if len(scope.Hosts) == 0 {
		return fmt.Errorf("reset task target scope is empty")
	}
	if err := validateScopedTaskFile(root, filepath.Join("tasks", entry), map[string]bool{}, scope); err != nil {
		return err
	}
	// Handlers can run after any task through notify/flush_handlers. Includes in
	// handlers use the same recursive task validation as the action entry.
	return filepath.WalkDir(filepath.Join(root, "handlers"), func(path string, item fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == filepath.Join(root, "handlers") {
			return nil
		}
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("reset handlers cannot be symlinks")
		}
		if filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return validateScopedTaskFile(root, relative, map[string]bool{}, scope)
	})
}

func validateScopedTaskFile(root, relative string, visiting map[string]bool, scope *TaskTargetScope) error {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || (!strings.HasPrefix(filepath.ToSlash(clean), "tasks/") && !strings.HasPrefix(filepath.ToSlash(clean), "handlers/")) {
		return fmt.Errorf("reset task include escapes role tasks or handlers")
	}
	if visiting[clean] {
		return fmt.Errorf("recursive task include: %s", clean)
	}
	visiting[clean] = true
	defer delete(visiting, clean)
	data, err := os.ReadFile(filepath.Join(root, clean))
	if err != nil {
		return err
	}
	return validateTasks(data, false, func(target string) error {
		candidate := filepath.Join(filepath.Dir(clean), target)
		if _, err := os.Stat(filepath.Join(root, candidate)); os.IsNotExist(err) {
			candidate = filepath.Join("tasks", target)
		}
		return validateScopedTaskFile(root, candidate, visiting, scope)
	}, scope)
}

func (r *Runner) ValidateTaskTargets(playbooks []string, scope TaskTargetScope) error {
	seen := map[string]bool{}
	for _, relative := range playbooks {
		if seen[relative] {
			continue
		}
		seen[relative] = true
		root, path, clean, err := r.resolvePlaybook(relative)
		if err != nil {
			return err
		}
		role := workspaceRoot(root, path, clean)
		entry, err := filepath.Rel(filepath.Join(role, "tasks"), path)
		if err != nil {
			return err
		}
		if err := validateRoleTaskTargets(role, entry, &scope); err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
	}
	return nil
}
