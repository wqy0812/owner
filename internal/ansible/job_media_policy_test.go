package ansible

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeMediaPolicyBeforeSideEffects(t *testing.T) {
	plugins, err := filepath.Abs("job_plugins")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-B", "testdata/media_policy_test.py", plugins)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native media policy: %v\n%s", err, output)
	}
}
