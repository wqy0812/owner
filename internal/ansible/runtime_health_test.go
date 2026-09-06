package ansible

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExecutorHealthMissingBinary(t *testing.T) {
	r := Runner{Binary: t.TempDir() + "/missing-ansible"}
	if err := r.CheckRuntime(context.Background()); err == nil {
		t.Fatal("missing executor passed")
	}
}
func TestExecutorHealthRealRuntimePlugins(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires local real Ansible fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := Runner{Binary: binary}
	if err := r.CheckRuntime(ctx); err != nil {
		output, _ := exec.Command(binary, "--version").CombinedOutput()
		t.Fatalf("plugins unavailable: %v\n%s", err, output)
	}
}
