package imagebuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex/platform-demo/internal/delivery"
)

type Docker struct{ root, binary string }

func NewDocker(root, binary string) *Docker {
	root, binary = strings.TrimSpace(root), strings.TrimSpace(binary)
	if root == "" {
		root = filepath.Join(os.TempDir(), "newplatform-image-builds")
	}
	if binary == "" {
		binary = "docker"
	}
	return &Docker{root: root, binary: binary}
}

func (d *Docker) Build(ctx context.Context, request Request, log func(LogEvent)) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(d.root, 0o700); err != nil {
		return Result{}, err
	}
	workdir, err := os.MkdirTemp(d.root, "image-build-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workdir)
	if err := os.Chmod(workdir, 0o700); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), request.Dockerfile, 0o600); err != nil {
		return Result{}, err
	}
	var logMu sync.Mutex
	emit := func(event LogEvent) {
		logMu.Lock()
		defer logMu.Unlock()
		if log != nil {
			log(event)
		}
	}
	for _, args := range [][]string{
		{"build", "--progress=plain", "--tag", request.ImageRef, "--file", "Dockerfile", "."},
		{"push", request.ImageRef},
	} {
		emit(LogEvent{Stream: "system", Message: "docker " + args[0] + " started"})
		cmd := exec.CommandContext(ctx, d.binary, args...)
		cmd.Dir = workdir
		// Let os/exec drain both streams before Wait returns. A child inheriting a
		// pipe cannot keep a cancelled build alive indefinitely.
		cmd.WaitDelay = 5 * time.Second
		stdout, stderr := &lineOutput{stream: "stdout", emit: emit}, &lineOutput{stream: "stderr", emit: emit}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		err := cmd.Run()
		stdout.flush()
		stderr.flush()
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if err != nil {
			return Result{}, err
		}
	}
	cmd := exec.CommandContext(ctx, d.binary, "image", "inspect", "--format={{index .RepoDigests 0}}", request.ImageRef)
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect pushed image digest: %w", err)
	}
	resolved := strings.TrimSpace(string(output))
	if !strings.Contains(resolved, "@sha256:") {
		return Result{}, errors.New("registry did not return an immutable image digest")
	}
	digest, err := delivery.DigestFromResolvedImageRef(resolved)
	if err != nil {
		return Result{}, err
	}
	return Result{ResolvedRef: resolved, Digest: digest}, nil
}

// Keep a bounded prefix of each line while still draining arbitrarily long
// output. Docker stdout/stderr may write concurrently; emit serializes callbacks.
type lineOutput struct {
	stream    string
	emit      func(LogEvent)
	pending   []byte
	truncated bool
}

func (w *lineOutput) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		room := 16*1024 - len(w.pending)
		if len(part) > room {
			w.pending = append(w.pending, part[:room]...)
			w.truncated = true
		} else {
			w.pending = append(w.pending, part...)
		}
		if end < 0 {
			break
		}
		w.line()
		p = p[end+1:]
	}
	return n, nil
}
func (w *lineOutput) line() {
	message := strings.TrimSuffix(string(w.pending), "\r")
	if w.truncated {
		message += " [truncated]"
	}
	w.emit(LogEvent{Stream: w.stream, Message: message})
	w.pending = w.pending[:0]
	w.truncated = false
}
func (w *lineOutput) flush() {
	if len(w.pending) > 0 || w.truncated {
		w.line()
	}
}
