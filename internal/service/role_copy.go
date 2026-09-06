package service

import yaml "gopkg.in/yaml.v3"

import (
	"path"
	"strings"
)

// Only task include operands change when generated check entrypoints receive
// new IDs. Module data, templates and ordinary text retain their exact values.
func rebaseRoleIncludes(filename string, content []byte, renamed map[string]string, exists func(string) bool) ([]byte, error) {
	if (!strings.HasPrefix(filename, "tasks/") && !strings.HasPrefix(filename, "handlers/")) || (path.Ext(filename) != ".yml" && path.Ext(filename) != ".yaml") {
		return content, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, err
	}
	changed := false
	var tasks func(*yaml.Node)
	tasks = func(node *yaml.Node) {
		if node.Kind == yaml.SequenceNode || node.Kind == yaml.DocumentNode {
			for _, child := range node.Content {
				tasks(child)
			}
			return
		}
		if node.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := strings.TrimPrefix(node.Content[i].Value, "ansible.builtin."), node.Content[i+1]
			if key == "block" || key == "always" || key == "rescue" {
				tasks(value)
			}
			if key != "include_tasks" && key != "import_tasks" {
				continue
			}
			operand := value
			if value.Kind == yaml.MappingNode {
				for j := 0; j < len(value.Content); j += 2 {
					if value.Content[j].Value == "file" {
						operand = value.Content[j+1]
					}
				}
			}
			if operand.Kind != yaml.ScalarNode {
				continue
			}
			candidate := path.Join(path.Dir(filename), operand.Value)
			if !exists(candidate) {
				candidate = path.Join("tasks", operand.Value)
			}
			next := renamed[candidate]
			if next == "" || next == candidate {
				continue
			}
			// Renaming an entrypoint preserves its parent directory. Keep a local
			// include local; otherwise retain Ansible's role task-root lookup.
			base := path.Dir(filename) + "/"
			if strings.HasPrefix(next, base) {
				operand.Value = strings.TrimPrefix(next, base)
			} else {
				operand.Value = strings.TrimPrefix(next, "tasks/")
			}
			changed = true
		}
	}
	tasks(&document)
	if !changed {
		return content, nil
	}
	return yaml.Marshal(&document)
}
