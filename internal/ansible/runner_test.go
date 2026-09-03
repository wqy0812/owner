package ansible

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunnerRunsThreePhasesAndCleansPrivateWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "site.yml"), "---\n- hosts: all\n  tasks: []\n", 0o600)
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	writeTestFile(t, binary, `#!/bin/sh
mode=execute
inventory=
vars=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -i) inventory="$2"; shift 2 ;;
    --extra-vars) vars="${2#@}"; shift 2 ;;
    --syntax-check) mode=syntax-check; shift ;;
    --list-hosts) mode=list-hosts; shift ;;
    *) shift ;;
  esac
done
printf 'MODE=%s INVENTORY=%s VARS=%s LOCAL_TEMP=%s\n' "$mode" "$inventory" "$vars" "$ANSIBLE_LOCAL_TEMP"
printf 'password=s3cr3t\n'
if [ "$mode" = execute ]; then
  printf 'PLAY RECAP *********************************************************************\n'
  printf 'localhost : ok=3 changed=2 unreachable=0 failed=0 skipped=1 rescued=0 ignored=0\n'
fi
`, 0o700)

	workRoot := t.TempDir()
	runner := &Runner{
		AllowedRoot: root,
		WorkRoot:    workRoot,
		Binary:      binary,
		MaxLogBytes: 1 << 20,
	}
	var inventoryPath, varsPath, localTemp string
	result, err := runner.Run(context.Background(), Request{
		Playbook:  "site.yml",
		Inventory: []byte("[all]\nlocalhost ansible_connection=local ansible_ssh_pass=s3cr3t\n"),
		Variables: map[string]any{"password": "s3cr3t", "ordinary": "visible"},
		Timeout:   5 * time.Second,
		LogSink: func(event LogEvent) {
			if !strings.Contains(event.Line, "INVENTORY=") || inventoryPath != "" {
				return
			}
			for _, field := range strings.Fields(event.Line) {
				key, value, ok := strings.Cut(field, "=")
				if !ok {
					continue
				}
				switch key {
				case "INVENTORY":
					inventoryPath = value
				case "VARS":
					varsPath = value
				case "LOCAL_TEMP":
					localTemp = value
				}
			}
			assertMode(t, inventoryPath, 0o600)
			assertMode(t, varsPath, 0o600)
			assertMode(t, localTemp, 0o700)
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Successful || len(result.Phases) != 3 {
		t.Fatalf("unexpected result: successful=%v phases=%d", result.Successful, len(result.Phases))
	}
	if got := result.Recap["localhost"]; got.OK != 3 || got.Changed != 2 || got.Failed != 0 {
		t.Fatalf("unexpected recap: %+v", got)
	}
	for _, event := range result.Logs {
		if strings.Contains(event.Line, "s3cr3t") {
			t.Fatalf("secret leaked in event: %q", event.Line)
		}
	}
	if inventoryPath == "" || varsPath == "" || localTemp == "" {
		t.Fatalf("fake runner did not expose workspace paths")
	}
	for _, path := range []string{inventoryPath, varsPath, localTemp} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("workspace path %s still exists, stat error=%v", path, err)
		}
	}
}

func TestRunnerTimeoutTerminatesProcessGroup(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "site.yml"), "---\n- hosts: all\n", 0o600)
	binary := filepath.Join(t.TempDir(), "slow-ansible-playbook")
	writeTestFile(t, binary, `#!/bin/sh
trap 'exit 143' TERM
sleep 10 &
wait
`, 0o700)
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary, KillGrace: 50 * time.Millisecond}
	started := time.Now()
	result, err := runner.Run(context.Background(), Request{
		Playbook:  "site.yml",
		Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"),
		Timeout:   150 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if !result.TimedOut || result.Successful {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation took too long: %v", elapsed)
	}
}

func TestRunnerDrainsSlowLogSinkWithoutFailingSuccessfulProcess(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "site.yml"), "---\n- hosts: all\n  tasks: []\n", 0o600)
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	writeTestFile(t, binary, `#!/bin/sh
case "$*" in *--syntax-check*|*--list-hosts*) exit 0 ;; esac
printf 'task output\n'
printf 'PLAY RECAP *********************************************************************\n'
printf 'localhost : ok=3 changed=2 unreachable=0 failed=0 skipped=0 rescued=0 ignored=0\n'
printf 'last stderr line\n' >&2
`, 0o700)
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary, KillGrace: 10 * time.Millisecond}
	var persisted []LogEvent
	result, err := runner.Run(context.Background(), Request{
		Playbook: "site.yml", Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"), Timeout: 5 * time.Second,
		LogSink: func(event LogEvent) {
			if event.Line == "task output" {
				time.Sleep(150 * time.Millisecond)
			}
			persisted = append(persisted, event)
		},
	})
	if err != nil || !result.Successful || result.Recap["localhost"].OK != 3 {
		t.Fatalf("successful process with slow persistence: result=%+v error=%v", result, err)
	}
	var recap, stderr, finished bool
	for _, event := range persisted {
		recap = recap || strings.HasPrefix(event.Line, "localhost :")
		stderr = stderr || event.Line == "last stderr line"
		finished = finished || event.Line == "finished execute: successful"
	}
	if !recap || !stderr || !finished {
		t.Fatalf("Run returned before persistence drained: recap=%v stderr=%v finished=%v", recap, stderr, finished)
	}
}

func TestRunnerDoesNotWaitForInheritedOutputHandles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "site.yml"), "---\n- hosts: all\n  tasks: []\n", 0o600)
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	writeTestFile(t, binary, `#!/bin/sh
case "$*" in *--syntax-check*|*--list-hosts*) exit 0 ;; esac
sleep 10 &
echo $! > "$HELPER_PID_FILE"
printf 'PLAY RECAP *********************************************************************\n'
printf 'localhost : ok=1 changed=0 unreachable=0 failed=0 skipped=0 rescued=0 ignored=0\n'
`, 0o700)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			}
		}
	})
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary, KillGrace: 10 * time.Millisecond, Env: map[string]string{"HELPER_PID_FILE": pidFile}}
	started := time.Now()
	result, err := runner.Run(context.Background(), Request{Playbook: "site.yml", Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"), Timeout: time.Second})
	if err != nil || !result.Successful || result.Recap["localhost"].OK != 1 {
		t.Fatalf("inherited output handle changed foreground result: result=%+v error=%v", result, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("waited for an idle background helper instead of the Ansible process")
	}
}

func TestRunnerRejectsSuccessfulExecuteWithoutHostRecap(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "site.yml"), "---\n- hosts: all\n  tasks: []\n", 0o600)
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	writeTestFile(t, binary, "#!/bin/sh\nprintf 'PLAY RECAP *********************************************************************\\n'\n", 0o700)
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary}
	result, err := runner.Run(context.Background(), Request{
		Playbook: "site.yml", Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"),
	})
	if !errors.Is(err, ErrNoHostRecap) {
		t.Fatalf("Run() error = %v, want ErrNoHostRecap", err)
	}
	if result.Successful {
		t.Fatalf("Run() successful = true, want false")
	}
}

func TestResolvePlaybookRejectsEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yml")
	writeTestFile(t, filepath.Join(root, "inside.yml"), "---\n", 0o600)
	writeTestFile(t, outside, "---\n", 0o600)
	runner := &Runner{AllowedRoot: root}

	for _, path := range []string{"../outside.yml", outside} {
		if _, _, _, err := runner.resolvePlaybook(path); !errors.Is(err, ErrOutsideRoot) && !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("resolvePlaybook(%q) error = %v", path, err)
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "link.yml")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := runner.resolvePlaybook("link.yml"); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("symlink escape error = %v", err)
		}
	}
}

func TestRedactorHandlesExplicitStructuredAndPrivateKeySecrets(t *testing.T) {
	redactor := NewRedactor(
		[]string{"literal-secret"},
		map[string]any{"nested": map[string]any{"access_token": "nested-secret", "K8S_ENCRYPTION_KEY": "encryption-secret"}},
		[]byte("host ansible_ssh_pass=inventory-secret\n"),
	)
	lines := []string{
		`literal-secret nested-secret inventory-secret`,
		`{"password":"json-secret"}`,
		`token: yaml-secret`,
		`Authorization: Bearer bearer-secret`,
		`https://user:url-secret@example.invalid/v1`,
		`-----BEGIN PRIVATE KEY-----`,
		`base64-private-material`,
		`-----END PRIVATE KEY-----`,
	}
	joined := ""
	for _, line := range lines {
		joined += redactor.Redact(line) + "\n"
	}
	for _, secret := range []string{"literal-secret", "nested-secret", "encryption-secret", "inventory-secret", "json-secret", "yaml-secret", "bearer-secret", "url-secret", "base64-private-material"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %q leaked in %q", secret, joined)
		}
	}
}

func TestLogCollectorBoundsPersistentSinkAndEmitsOneMarker(t *testing.T) {
	var persisted []LogEvent
	collector := newLogCollector(5, func(event LogEvent) { persisted = append(persisted, event) }, NewRedactor(nil, nil, nil))
	collector.Emit(PhaseExecute, StreamStdout, "abc")
	collector.Emit(PhaseExecute, StreamStdout, "def")
	collector.Emit(PhaseExecute, StreamStderr, "ghi")
	logs, truncated := collector.Snapshot()
	if !truncated || len(logs) != 1 || logs[0].Line != "abc" {
		t.Fatalf("snapshot logs=%+v truncated=%v", logs, truncated)
	}
	if len(persisted) != 2 || persisted[0].Line != "abc" || persisted[1].Stream != StreamSystem || !strings.Contains(persisted[1].Line, "truncated") {
		t.Fatalf("persisted sink events=%+v", persisted)
	}
}

func TestRecapParser(t *testing.T) {
	parser := newRecapParser()
	parser.Add("task output: ok=99 changed=99 unreachable=0 failed=0")
	if len(parser.Snapshot()) != 0 {
		t.Fatalf("task output before PLAY RECAP was accepted: %+v", parser.Snapshot())
	}
	parser.Add("PLAY RECAP *********************************************************************")
	parser.Add("localhost : ok=7 changed=2 unreachable=0 failed=1 skipped=3 rescued=1 ignored=0")
	parser.Add("not a recap: changed=9")
	got := parser.Snapshot()["localhost"]
	want := (HostRecap{OK: 7, Changed: 2, Failed: 1, Skipped: 3, Rescued: 1})
	if got != want {
		t.Fatalf("recap = %+v, want %+v", got, want)
	}
}

func TestCommandEnvForcesDefaultStdoutCallback(t *testing.T) {
	t.Setenv("ANSIBLE_STDOUT_CALLBACK", "minimal")
	runner := &Runner{Env: map[string]string{"ANSIBLE_STDOUT_CALLBACK": "json"}}
	got := map[string]string{}
	for _, item := range runner.commandEnv(t.TempDir()) {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			got[key] = value
		}
	}
	if got["ANSIBLE_STDOUT_CALLBACK"] != "default" {
		t.Fatalf("stdout callback=%q, want default", got["ANSIBLE_STDOUT_CALLBACK"])
	}
}

func TestTreeDigestIsDeterministicAndContentSensitive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "file.yml")
	writeTestFile(t, path, "one", 0o600)
	first, err := TreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TreeDigest(root)
	if err != nil || first != second {
		t.Fatalf("digest not deterministic: first=%s second=%s err=%v", first, second, err)
	}
	writeTestFile(t, path, "two", 0o600)
	third, err := TreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("digest did not change with file content")
	}
}

func TestRunnerRejectsChangedLockedArtifactBeforeExecution(t *testing.T) {
	root := t.TempDir()
	playbook := filepath.Join(root, "site.yml")
	writeTestFile(t, playbook, "---\n- hosts: all\n  tasks: []\n", 0o600)
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir()}
	playbookDigest, treeDigest, err := runner.Digest("site.yml")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	writeTestFile(t, binary, "#!/bin/sh\ntouch \""+marker+"\"\n", 0o700)
	runner.Binary = binary
	writeTestFile(t, playbook, "---\n- hosts: all\n  tasks:\n    - debug: msg=changed\n", 0o600)

	_, err = runner.Run(context.Background(), Request{
		Playbook: "site.yml", Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"),
		ExpectedPlaybookSHA256: playbookDigest, ExpectedTreeSHA256: treeDigest,
	})
	if !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("Run error=%v, want ErrArtifactChanged", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Ansible binary executed despite digest mismatch: stat=%v", statErr)
	}
}

func TestPreparedWorkspaceIsSharedAcrossSteps(t *testing.T) {
	root := t.TempDir()
	for _, playbook := range []string{"one.yml", "two.yml"} {
		writeTestFile(t, filepath.Join(root, playbook), "---\n- hosts: all\n  tasks: []\n", 0o600)
	}
	binary := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	writeTestFile(t, binary, "#!/bin/sh\nprintf 'PLAY RECAP *********************************************************************\\n'\nprintf 'localhost : ok=1 changed=0 unreachable=0 failed=0 skipped=0 rescued=0 ignored=0\\n'\n", 0o700)
	runner := &Runner{AllowedRoot: root, WorkRoot: t.TempDir(), Binary: binary}
	digests, treeDigest, err := runner.DigestPlan([]string{"one.yml", "two.yml"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := runner.PrepareWorkspace(treeDigest)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := workspace.path
	defer workspace.Close()
	// The source is no longer consulted once the Run workspace is prepared.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, playbook := range []string{"one.yml", "two.yml"} {
		result, err := runner.RunInWorkspace(context.Background(), workspace, Request{
			Playbook: playbook, Inventory: []byte("[all]\nlocalhost ansible_connection=local\n"),
			ExpectedPlaybookSHA256: digests[playbook], ExpectedTreeSHA256: treeDigest,
		})
		if err != nil || !result.Successful {
			t.Fatalf("RunInWorkspace(%s) result=%+v err=%v", playbook, result, err)
		}
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspacePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared workspace was not cleaned: %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", path, got, want)
	}
}
