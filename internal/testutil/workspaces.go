// Package testutil builds disposable current-contract fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

const Playbook = "---\n- hosts: all\n  tasks: []\n"

// Workspaces installs synthetic Playbooks for fixture Releases. Call only before
// creating test evidence: the workspace is part of the Release contract digest.
func Workspaces(t testing.TB, db *store.Store, root string, releaseIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if len(releaseIDs) == 0 {
		rows, err := db.DB().Query("SELECT id FROM component_releases ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			releaseIDs = append(releaseIDs, id)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range releaseIDs {
		r, err := db.GetComponentRelease(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Actions) == 0 {
			continue
		}
		prefix := "managed/fixtures/" + id + "/"
		if r.PlaybookWorkspaceRoot != "" {
			prefix = r.PlaybookWorkspaceRoot
		}
		dir := filepath.Join(root, filepath.FromSlash(prefix))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		files := []domain.ComponentPlaybookFile{}
		for _, a := range r.Actions {
			path := string(a.Kind) + ".yml"
			relative := prefix + path
			content := []byte(Playbook)
			if existing, e := os.ReadFile(filepath.Join(dir, path)); e == nil {
				content = existing
			}
			if err := os.WriteFile(filepath.Join(dir, path), content, 0600); err != nil {
				t.Fatal(err)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(content))
			files = append(files, domain.ComponentPlaybookFile{ReleaseID: id, Path: path, SHA256: sum, SizeBytes: int64(len(content)), MediaType: "application/yaml", UpdatedAt: time.Now().UTC()})
			if _, err := db.DB().Exec("UPDATE action_definitions SET playbook=?,playbook_sha256=? WHERE id=?", relative, sum, a.ID); err != nil {
				t.Fatal(err)
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		var tree strings.Builder
		if _, err := db.DB().Exec("DELETE FROM component_playbook_files WHERE release_id=?", id); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			tree.WriteString(f.Path + "\x00" + f.SHA256 + "\x00")
			if _, err := db.DB().Exec("INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES(?,?,?,?,?,?)", id, f.Path, f.SHA256, f.SizeBytes, f.MediaType, f.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.DB().Exec("UPDATE component_releases SET playbook_workspace_root=?,playbook_tree_sha256=? WHERE id=?", prefix, fmt.Sprintf("%x", sha256.Sum256([]byte(tree.String()))), id); err != nil {
			t.Fatal(err)
		}
	}
}
