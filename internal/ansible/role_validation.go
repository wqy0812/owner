package ansible

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateRoleTasks checks executable task keywords, without interpreting module
// argument dictionaries as tasks. Commands declared read-only still require
// owner review: changed_when does not sandbox a shell command.
func ValidateRoleTasks(content []byte, check bool) error {
	return validateTasks(content, check, nil)
}
func validateTasks(content []byte, check bool, include func(string) error) error {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("invalid role tasks: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode || len(document.Content[0].Content) == 0 {
		return fmt.Errorf("role entry must contain a non-empty YAML task list")
	}
	var aliases func(*yaml.Node) error
	aliases = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			return fmt.Errorf("YAML aliases are not allowed in executable source")
		}
		if n.Kind == yaml.ScalarNode {
			if err := validateLocalLookups(n.Value); err != nil {
				return err
			}
		}
		for _, child := range n.Content {
			if err := aliases(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := aliases(&document); err != nil {
		return err
	}
	var tasks func(*yaml.Node) error
	tasks = func(list *yaml.Node) error {
		if list.Kind != yaml.SequenceNode {
			return fmt.Errorf("task block must be a list")
		}
		for _, task := range list.Content {
			if task.Kind != yaml.MappingNode {
				return fmt.Errorf("each task must be a mapping")
			}
			values := map[string]*yaml.Node{}
			for i := 0; i < len(task.Content); i += 2 {
				values[task.Content[i].Value] = task.Content[i+1]
			}
			for key, value := range values {
				module := strings.TrimPrefix(key, "ansible.builtin.")
				if module == "copy" || module == "template" || module == "unarchive" || module == "script" {
					if err := validateLocalSource(module, value); err != nil {
						return err
					}
					if args := values["args"]; args != nil {
						if err := validateLocalSource(module, args); err != nil {
							return err
						}
					}
				}
				switch module {
				case "hosts", "roles", "import_playbook", "strategy", "force_handlers", "rescue", "local_action", "action", "any_errors_fatal", "async", "include_role", "import_role", "add_host", "group_by", "set_stats", "include_vars":
					return fmt.Errorf("%s is not permitted in managed role tasks", key)
				case "block", "always":
					if err := tasks(value); err != nil {
						return err
					}
				case "ignore_errors", "ignore_unreachable":
					if value.Tag != "!!bool" || value.Value != "false" {
						return fmt.Errorf("%s cannot bypass job failure", key)
					}
				case "meta":
					if value.Value != "flush_handlers" || check {
						return fmt.Errorf("meta %s can bypass job boundaries", value.Value)
					}
				case "register":
					if reservedRoleVariable(value.Value) {
						return fmt.Errorf("%s is a reserved variable", value.Value)
					}
				case "vars", "set_fact":
					if value.Kind == yaml.MappingNode {
						for i := 0; i < len(value.Content); i += 2 {
							if reservedRoleVariable(value.Content[i].Value) {
								return fmt.Errorf("%s is a reserved variable", value.Content[i].Value)
							}
						}
					}
				case "include_tasks", "import_tasks":
					target := value.Value
					if value.Kind == yaml.MappingNode {
						for i := 0; i < len(value.Content); i += 2 {
							if value.Content[i].Value == "file" {
								target = value.Content[i+1].Value
							}
						}
					}
					if target == "" || strings.Contains(target, "{{") || filepath.IsAbs(target) || strings.Contains(target, "..") {
						return fmt.Errorf("task includes must use a fixed relative path inside this role")
					}
					if include != nil {
						if err := include(target); err != nil {
							return err
						}
					}
				}
				if check {
					switch module {
					case "copy", "template", "file", "package", "apt", "yum", "dnf", "service", "systemd", "systemd_service", "unarchive", "get_url", "lineinfile", "blockinfile", "user", "group", "mount", "reboot", "notify":
						return fmt.Errorf("check actions cannot use %s", key)
					case "command", "shell", "raw", "script":
						if v := values["changed_when"]; v == nil || v.Tag != "!!bool" || v.Value != "false" {
							return fmt.Errorf("check command must declare changed_when: false and only inspect state")
						}
					}
				}
			}
		}
		return nil
	}
	return tasks(document.Content[0])
}

var rolePathPrefix = regexp.MustCompile(`^\{\{\s*role_path\s*\}\}/`)
var localLookupCall = regexp.MustCompile(`\b(?:lookup|query|q)\s*\(\s*['"](?:ansible\.builtin\.)?(?:file|fileglob|template|first_found)['"]\s*,([^)]*)\)`)
var fixedLookupArgument = regexp.MustCompile(`^\s*['"]([^'"]+)['"]\s*(?:,\s*[a-zA-Z_]\w*\s*=.*)?$`)

func validateLocalLookups(value string) error {
	for _, call := range localLookupCall.FindAllStringSubmatch(value, -1) {
		argument := fixedLookupArgument.FindStringSubmatch(call[1])
		if len(argument) != 2 || !safeLocalRolePath(argument[1]) {
			return fmt.Errorf("local file lookups must use one fixed relative path inside this role")
		}
	}
	return nil
}

func safeLocalRolePath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.Contains(path, "{{") && !strings.Contains(path, "..") && !strings.ContainsAny(path, "\\\x00~") && !strings.HasPrefix(filepath.Clean(path), "roles/")
}

func validateLocalSource(module string, node *yaml.Node) error {
	source, remote := "", false
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if key == "src" || (module == "script" && key == "cmd") {
				source = value.Value
			}
			if key == "remote_src" {
				remote = value.Tag == "!!bool" && value.Value == "true"
			}
		}
	} else if module == "script" {
		source = node.Value
	} else {
		return fmt.Errorf("%s must use YAML mapping arguments so its local source can be validated", module)
	}
	if remote || source == "" {
		return nil
	}
	source = rolePathPrefix.ReplaceAllString(source, "")
	if module == "script" {
		source = strings.SplitN(source, " ", 2)[0]
	}
	if !safeLocalRolePath(source) {
		return fmt.Errorf("%s source must use a fixed relative path inside this role; target-host sources must declare remote_src: true", module)
	}
	return nil
}
func reservedRoleVariable(name string) bool {
	return name == "cf" || (strings.HasPrefix(name, "cf_") && name != "cf_local" && !strings.HasPrefix(name, "cf_local_")) || strings.HasPrefix(name, "ansible_")
}

func validateRoleEntry(root, relative string, check bool, visiting map[string]bool) error {
	path := filepath.Clean(relative)
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return fmt.Errorf("task include escapes role")
	}
	if visiting[path] {
		return fmt.Errorf("recursive task include: %s", path)
	}
	visiting[path] = true
	defer delete(visiting, path)
	data, err := os.ReadFile(filepath.Join(root, "tasks", path))
	if err != nil {
		return err
	}
	return validateTasks(data, check, func(target string) error {
		candidate := filepath.Join(filepath.Dir(path), target)
		if _, err := os.Stat(filepath.Join(root, "tasks", candidate)); os.IsNotExist(err) {
			candidate = target
		}
		return validateRoleEntry(root, candidate, check, visiting)
	})
}

func validateRoleVariables(data []byte) error {
	if err := validateLocalLookups(string(data)); err != nil {
		return err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("role defaults and vars must be mappings")
	}
	for i := 0; i < len(node.Content[0].Content); i += 2 {
		name := node.Content[0].Content[i]
		if name.Value == "<<" || reservedRoleVariable(name.Value) {
			return fmt.Errorf("role cannot override reserved variable %s", name.Value)
		}
	}
	return nil
}
