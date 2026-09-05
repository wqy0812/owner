package ansible

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registered variables and nonpersistent facts survive Ansible plays. Clear
// every owner-written name at each phase so only cf.inputs crosses boundaries.
func roleLocalVariables(directory string) (map[string]any, error) {
	names := map[string]any{}
	err := filepath.WalkDir(filepath.Join(directory, "roles"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(filepath.Join(directory, "roles"), path)
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 3 || (parts[1] != "tasks" && parts[1] != "handlers") {
			return nil
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml")) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			return err
		}
		var walk func(*yaml.Node)
		walk = func(n *yaml.Node) {
			if n.Kind == yaml.MappingNode {
				for i := 0; i < len(n.Content); i += 2 {
					key, value := n.Content[i].Value, n.Content[i+1]
					if key == "register" && value.Kind == yaml.ScalarNode && !reservedRoleVariable(value.Value) {
						names[value.Value] = nil
					}
					if strings.TrimPrefix(key, "ansible.builtin.") == "set_fact" && value.Kind == yaml.MappingNode {
						for j := 0; j < len(value.Content); j += 2 {
							name := value.Content[j].Value
							if name != "cacheable" && !reservedRoleVariable(name) {
								names[name] = nil
							}
						}
					}
				}
			}
			for _, child := range n.Content {
				walk(child)
			}
		}
		walk(&node)
		return nil
	})
	return names, err
}
