package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/testutil"
)

func TestRoleCopiesRebaseCheckIncludes(t *testing.T) {
	for _, mode := range []string{"import", "clone"} {
		t.Run(mode, func(t *testing.T) {
			p, db, root := componentImportRecoveryPlatform(t)
			ctx := context.Background()
			if err := (&seed.Seeder{Store: db}).Run(ctx); err != nil {
				t.Fatal(err)
			}
			owner, err := db.GetUser(ctx, "component-import-owner")
			if err != nil {
				t.Fatal(err)
			}
			entry := ComponentImportEntry{}
			entry.Component.Name, entry.Component.Slug, entry.Component.Layer, entry.Component.Tags = "Copy", "copy", domain.LayerRuntimeState, []string{"runtime"}
			entry.Release.Version, entry.Release.LineName, entry.Release.ReleaseNotes = "1.0", "Baseline", "Copy fixture"
			entry.Release.Actions = []ComponentImportAction{
				{ID: "install", Name: "Install", Type: domain.ActionInstall, Playbook: "tasks/install.yml", PreCheckActionID: "check", PostCheckActionID: "check", HostGroup: "all"},
				{ID: "check", Name: "Check", Type: domain.ActionCheck, Playbook: "tasks/checks/check.yml", HostGroup: "all"},
			}
			entry.Playbooks = []ComponentImportFileSource{
				{"tasks/install.yml", "- name: Run included check\n  ansible.builtin.include_tasks:\n    file: checks/check.yml\n- ansible.builtin.debug:\n    msg: checks/check.yml\n"},
				{"tasks/checks/check.yml", testutil.Playbook},
			}
			input := ComponentImportRequest{Entries: []ComponentImportEntry{entry}}
			prepared, err := p.catalog.prepareComponentImport(ctx, owner, input, ComponentImportPlan{Order: []string{"copy"}})
			if err != nil {
				t.Fatal(err)
			}
			release := prepared.Releases[0]
			files := prepared.Files
			if mode == "clone" {
				component := prepared.Components[0]
				if err := db.CreateComponent(ctx, component); err != nil {
					t.Fatal(err)
				}
				if err := db.CreateComponentRelease(ctx, release); err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					target := filepath.Join(root, file.RelativePath)
					if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, []byte(file.Content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// Make a valid source with an include of its persisted check ID.
				checkPath, _ := domain.ActionTaskPath(release.Actions[1])
				installPath := filepath.Join(root, release.Actions[0].Playbook)
				content := "- ansible.builtin.import_tasks: " + strings.TrimPrefix(checkPath, "tasks/") + "\n- ansible.builtin.debug:\n    msg: checks/check.yml\n"
				if err := os.WriteFile(installPath, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				testutil.Workspaces(t, db, root, release.ID)
				source, err := db.GetComponentRelease(ctx, release.ID)
				if err != nil {
					t.Fatal(err)
				}
				release = source
				release.ID, release.Version, release.CreatedAt = "cloned-release", "2.0", time.Now().UTC()
				release.PlaybookWorkspaceRoot = generatedManagedReleasePrefix(component, release)
				for i := range release.Actions {
					release.Actions[i].ID = "cloned-" + release.Actions[i].ID
				}
				manifest, err := p.catalog.copyManagedPlaybooksForClone(component, source.ID, &release)
				if err != nil {
					t.Fatal(err)
				}
				defer p.catalog.cleanupComponentImportManifest(manifest, true)
				files = nil
				for _, file := range release.PlaybookFiles {
					content, err := os.ReadFile(filepath.Join(root, managedReleasePrefix(component, release), file.Path))
					if err != nil {
						t.Fatal(err)
					}
					files = append(files, componentImportFile{RelativePath: managedReleasePrefix(component, release) + file.Path, Content: string(content)})
				}
			}
			var install domain.ActionDefinition
			for _, action := range release.Actions {
				if action.Kind == domain.ActionInstall {
					install = action
				}
			}
			for _, file := range files {
				if file.RelativePath != install.Playbook {
					continue
				}
				var checkID string
				for _, action := range release.Actions {
					if action.Name == "Check" {
						checkID = action.ID
					}
				}
				if !strings.Contains(file.Content, "checks/"+checkID+".yml") {
					t.Fatalf("copied include still targets old check: %s", file.Content)
				}
				if !strings.Contains(file.Content, "msg: checks/check.yml") {
					t.Fatalf("rewrote unrelated module value: %s", file.Content)
				}
				if install.PlaybookSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(file.Content))) {
					t.Fatal("copied action hash does not match rewritten content")
				}
			}
		})
	}
}
