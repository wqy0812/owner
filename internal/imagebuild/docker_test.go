package imagebuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDockerBuildIsolationLogsAndFailures(t *testing.T) {
	for _, failure := range []string{"", "build", "push", "image", "mutable"} {
		t.Run("failure="+failure, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "work")
			commands := filepath.Join(dir, "commands")
			binary := filepath.Join(dir, "docker")
			t.Setenv("BUILD_COMMANDS", commands)
			t.Setenv("BUILD_FAILURE", failure)
			script := `#!/bin/sh
printf '%s\n' "$1" >> "$BUILD_COMMANDS"
[ "$1" != "$BUILD_FAILURE" ] || exit 9
case "$1" in
build)
 [ "$(cat Dockerfile)" = "FROM scratch" ] || exit 8
 printf 'first\nlast'
 printf 'warning\n' >&2
 ;;
push) printf 'pushed\n';;
image)
 if [ "$BUILD_FAILURE" = mutable ]; then printf 'registry.test/component:tag'; else
 printf 'registry.test/component@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'; fi
 ;;
esac
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var logs []LogEvent
			result, err := NewDocker(root, binary).Build(context.Background(), Request{ImageRef: "registry.test/component:tag", Dockerfile: []byte("FROM scratch\n")}, func(e LogEvent) { logs = append(logs, e) })
			if (err != nil) != (failure != "") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if failure == "" && result.Digest != "sha256:"+strings.Repeat("a", 64) {
				t.Fatal(result)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("workdir not cleaned: %v %v", entries, err)
			}
			b, err := os.ReadFile(commands)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"build", "push", "image"}
			if failure == "build" {
				want = want[:1]
			} else if failure == "push" {
				want = want[:2]
			}
			if !reflect.DeepEqual(strings.Fields(string(b)), want) {
				t.Fatalf("commands=%s", b)
			}
			if failure == "" {
				for _, want := range []LogEvent{{"stdout", "first"}, {"stdout", "last"}, {"stderr", "warning"}} {
					found := false
					for _, got := range logs {
						found = found || got == want
					}
					if !found {
						t.Fatalf("missing %v in %v", want, logs)
					}
				}
			}
		})
	}
}
func TestDockerCancellationCleansWorkspace(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	root := filepath.Join(dir, "work")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewDocker(root, binary).Build(ctx, Request{Dockerfile: []byte("FROM scratch\n")}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation=%v elapsed=%v", err, time.Since(start))
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace remains: %v %v", entries, err)
	}
}
func TestBuildOutputDrainsLongLinesAndUnterminatedTail(t *testing.T) {
	var logs []LogEvent
	w := &lineOutput{stream: "stderr", emit: func(e LogEvent) { logs = append(logs, e) }}
	input := strings.Repeat("x", 5<<20) + "\nlast"
	n, err := w.Write([]byte(input))
	w.flush()
	if err != nil || n != len(input) || len(logs) != 2 || len(logs[0].Message) != 16*1024+len(" [truncated]") || logs[1].Message != "last" {
		t.Fatalf("n=%d error=%v logs=%d", n, err, len(logs))
	}
}
