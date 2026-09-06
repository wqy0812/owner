package ansible

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestNativeJobRealCachedMediaFence(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	var restored atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if restored.Load() {
			_, _ = w.Write([]byte("locked media"))
		} else {
			_, _ = w.Write([]byte("replaced media"))
		}
	}))
	defer server.Close()
	runner, plan, target := roleJobFixture(t, binary, "")
	for i := range plan.Steps {
		plan.Steps[i].ParentActionID = "install.yml"
	}
	plan.Steps[4].Media = []JobMedia{{Kind: "artifact", Location: server.URL, Identity: fmt.Sprintf("%x", sha256.Sum256([]byte("locked media"))), SizeBytes: 12}}
	bundle, err := runner.BuildJob(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	results := filepath.Join(t.TempDir(), "results")
	if output, err := nativeCommand(t, binary, bundle, "install", results, nil); err == nil {
		t.Fatalf("changed cached media accepted: %s", output)
	}
	if _, err := os.Stat(filepath.Join(target, "alpha-one-a-body")); !os.IsNotExist(err) {
		t.Fatal("component mutated before media identity check")
	}
	restored.Store(true)
	if output, err := nativeCommand(t, binary, bundle, "resume", results, nil); err != nil {
		t.Fatalf("resume after exact media restoration: %v\n%s", err, output)
	}
}
