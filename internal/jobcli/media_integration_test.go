package jobcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"codex/platform-demo/internal/ansible"
)

func TestStandaloneRealMediaWaitsForSourceVerification(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	for _, pass := range []bool{false, true} {
		t.Run(fmt.Sprint(pass), func(t *testing.T) {
			root, target := t.TempDir(), t.TempDir()
			var calls atomic.Int32
			var transferred atomic.Bool
			checksum := fmt.Sprintf("%x", sha256.Sum256([]byte("payload")))
			station := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !pass {
					t.Error("media requested despite a failing source verification")
				}
				switch r.URL.Path {
				case "/api/v1/register":
					if !transferred.Load() {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.WriteHeader(http.StatusOK)
				case "/api/v1/fetch":
					transferred.Store(true)
					w.WriteHeader(http.StatusCreated)
					fmt.Fprintf(w, `{"relativePath":"a","sha256":"%s"}`, checksum)
				default:
					t.Errorf("unexpected media request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer station.Close()
			role := filepath.Join(root, "managed", "component", "line", "release")
			sources := map[string]string{
				"checks/source.yml": "- assert:\n    that: cf.inputs.source_ok\n",
				"install.yml":       "- copy:\n    dest: '{{ cf.inputs.target }}/installed'\n    content: installed\n",
			}
			for name, content := range sources {
				path := filepath.Join(role, "tasks", name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			tree, err := ansible.TreeDigest(role)
			if err != nil {
				t.Fatal(err)
			}
			plan := ansible.JobPlan{Inventory: "[nodes]\nlocal ansible_connection=local\n", Metadata: map[string]any{
				"steps":             []any{map[string]any{"id": "install"}},
				"artifactTransfers": []any{map[string]any{"requirementId": "a", "sourceUrl": "https://source.test/a", "targetStation": strings.TrimPrefix(station.URL, "http://"), "relativePath": "a", "sha256": checksum}},
			}}
			for i, name := range []string{"checks/source.yml", "install.yml"} {
				digest, err := ansible.FileDigest(filepath.Join(role, "tasks", name))
				if err != nil {
					t.Fatal(err)
				}
				step := ansible.JobStep{ID: name, NodeID: "node", ReleaseID: "release", ComponentID: "component", ActionID: name, Action: []string{"check", "install"}[i], Phase: []string{"check", "execute"}[i], Limit: "nodes", Playbook: "managed/component/line/release/tasks/" + name, PlaybookDigest: digest, WorkspaceDigest: tree, TimeoutSeconds: 15, Variables: map[string]any{"source_ok": pass, "target": target}}
				if i == 0 {
					step.Stage = "source_verify"
				}
				plan.Steps = append(plan.Steps, step)
			}
			runner := ansible.Runner{AllowedRoot: root, Binary: binary}
			bundle, err := runner.BuildJob(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			var out bytes.Buffer
			err = Run([]string{"run", bundle.Path, "--results", t.TempDir(), "--ansible", binary}, &out, &out)
			if pass {
				if err != nil || calls.Load() != 3 || !transferred.Load() {
					t.Fatalf("source passed: calls=%d transferred=%v error=%v\n%s", calls.Load(), transferred.Load(), err, out.String())
				}
				if _, err := os.Stat(filepath.Join(target, "installed")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || calls.Load() != 0 {
					t.Fatalf("failed source requested media: calls=%d error=%v\n%s", calls.Load(), err, out.String())
				}
				if _, err := os.Stat(filepath.Join(target, "installed")); !os.IsNotExist(err) {
					t.Fatal("mutation ran after source failure")
				}
			}
		})
	}
}
