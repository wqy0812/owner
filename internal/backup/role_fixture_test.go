package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

// Complete the synthetic published Releases using the current Role contract.
// All source files, including preexisting templates, enter the manifest.
func materializeBackupRoleFixture(t *testing.T, db *store.Store, root string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var admin string
	if err := db.DB().QueryRow(`SELECT id FROM users WHERE role='platform_admin' LIMIT 1`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	var group string
	if err := db.DB().QueryRow(`SELECT o.technical_value FROM platform_options o JOIN platform_option_categories c ON c.id=o.category_id WHERE c.category_type='host_group' ORDER BY o.id LIMIT 1`).Scan(&group); err != nil {
		if _, err = db.DB().Exec(`INSERT INTO platform_option_categories(id,technical_key,label,category_type,created_by,created_at) VALUES('backup-hosts','hostGroup','Host groups','host_group',?,?)`, admin, now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.DB().Exec(`INSERT INTO platform_options(id,category_id,technical_value,label,created_by,created_at) VALUES('backup-all','backup-hosts','all','All',?,?)`, admin, now); err != nil {
			t.Fatal(err)
		}
		group = "all"
	}
	rows, err := db.DB().Query(`SELECT id FROM component_releases ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		release, err := db.GetComponentRelease(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		actions := testutil.BoundFixtureActions(id, release.Actions)
		prefix := release.PlaybookWorkspaceRoot
		if prefix == "" {
			prefix = "managed/backup-fixtures/" + id + "/"
		}
		for _, action := range actions {
			entry, err := domain.ActionTaskPath(action)
			if err != nil {
				t.Fatal(err)
			}
			full := filepath.Join(root, filepath.FromSlash(prefix+entry))
			if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(full)
			if os.IsNotExist(err) {
				data = []byte(testutil.Playbook)
				err = os.WriteFile(full, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			host := action.HostGroup
			if host == "all" {
				host = group
			}
			if host == "" {
				host = group
			}
			_, err = db.DB().Exec(`INSERT INTO action_definitions(id,release_id,name,kind,playbook,playbook_sha256,host_group,timeout_seconds,risk_level,destructive,idempotent,pre_check_action_id,post_check_action_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET playbook=excluded.playbook,playbook_sha256=excluded.playbook_sha256,pre_check_action_id=excluded.pre_check_action_id,post_check_action_id=excluded.post_check_action_id`, action.ID, id, action.Name, action.Kind, prefix+entry, hex.EncodeToString(sum[:]), host, 60, "low", false, action.Idempotent, action.PreCheckActionID, action.PostCheckActionID)
			if err != nil {
				t.Fatal(err)
			}
		}
		files := []Playbook{}
		err = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(prefix)), func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(filepath.Join(root, filepath.FromSlash(prefix)), path)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(data)
			files = append(files, Playbook{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		if _, err = db.DB().Exec(`DELETE FROM component_playbook_files WHERE release_id=?`, id); err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if _, err = db.DB().Exec(`INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES(?,?,?,?,?,?)`, id, file.Path, file.SHA256, file.SizeBytes, "text/plain", now); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.DB().Exec(`UPDATE component_releases SET playbook_workspace_root=?,playbook_tree_sha256=? WHERE id=?`, prefix, acceptanceManifestDigest(files, ""), id); err != nil {
			t.Fatal(err)
		}
	}
}
