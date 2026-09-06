package service

import (
	"codex/platform-demo/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

func workspaceActionReferenced(release domain.ComponentRelease, relative string) bool {
	for _, action := range release.Actions {
		path, err := domain.ActionTaskPath(action)
		if err == nil && path == relative {
			return true
		}
	}
	return false
}

func workspaceReferences(release domain.ComponentRelease, files []domain.ComponentPlaybookFile, directory string) map[string]WorkspaceReference {
	refs := map[string]WorkspaceReference{}
	for _, file := range files {
		ref := WorkspaceReference{Actions: []WorkspaceActionReference{}, StaticReferences: []string{}, DynamicReferencesUnknown: true}
		for _, action := range release.Actions {
			path, err := domain.ActionTaskPath(action)
			if err != nil || path != file.Path {
				continue
			}
			uses := []string{}
			for _, parent := range release.Actions {
				if parent.PreCheckActionID == action.ID {
					uses = append(uses, parent.Name+" · 前置检查")
				}
				if parent.PostCheckActionID == action.ID {
					uses = append(uses, parent.Name+" · 后置检查")
				}
			}
			ref.Actions = append(ref.Actions, WorkspaceActionReference{ActionID: action.ID, ActionName: action.Name, UsedAs: uses})
			ref.ProtectionReason = "此文件被动作直接引用，请在动作编辑器维护"
		}
		refs[file.Path] = ref
	}
	// Literal references are useful warnings, never proof that a file is unused.
	for _, source := range files {
		if source.SizeBytes > MaxPlaybookBytes || !(strings.HasSuffix(source.Path, ".yml") || strings.HasSuffix(source.Path, ".yaml")) {
			continue
		}
		resolved, err := resolveWorkspaceReadPath(directory, source.Path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		for path, ref := range refs {
			if path == source.Path {
				continue
			}
			relative, _ := filepath.Rel(filepath.Dir(source.Path), path)
			for _, line := range strings.Split(string(data), "\n") {
				value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")), " ")
				key, val, ok := strings.Cut(value, ":")
				if !ok {
					continue
				}
				key = strings.TrimPrefix(strings.TrimSpace(key), "ansible.builtin.")
				if key != "import_tasks" && key != "include_tasks" && key != "src" && key != "file" {
					continue
				}
				val = strings.Trim(strings.TrimSpace(val), "\"'")
				if val == path || val == filepath.ToSlash(relative) {
					ref.StaticReferences = append(ref.StaticReferences, source.Path)
					break
				}
			}
			refs[path] = ref
		}
	}
	return refs
}
