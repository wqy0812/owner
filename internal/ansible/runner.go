package ansible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout     = 30 * time.Minute
	defaultKillGrace   = 3 * time.Second
	defaultMaxLogBytes = 2 << 20
	maxScannerToken    = 4 << 20
)

func (r *Runner) run(parent context.Context, req Request) (result Result, runErr error) {
	root, sourcePlaybook, cleanPlaybook, err := r.resolvePlaybook(req.Playbook)
	if err != nil {
		return Result{}, err
	}
	if len(req.Inventory) == 0 {
		return Result{}, fmt.Errorf("%w: inventory is required", ErrInvalidRequest)
	}
	if err := validateSelector(req.Limit, "limit"); err != nil {
		return Result{}, err
	}
	for _, tag := range req.Tags {
		if err := validateSelector(tag, "tag"); err != nil {
			return Result{}, err
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	result = Result{
		Playbook:  cleanPlaybook,
		StartedAt: time.Now().UTC(),
		Recap:     make(map[string]HostRecap),
	}
	defer func() {
		result.FinishedAt = time.Now().UTC()
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
		if errors.Is(ctx.Err(), context.Canceled) {
			result.Canceled = true
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
		}
		result.Successful = runErr == nil && !result.Canceled && !result.TimedOut
	}()

	result.TreeSHA256, err = TreeDigest(root)
	if err != nil {
		return result, fmt.Errorf("digest allowed tree: %w", err)
	}
	result.PlaybookSHA256, err = FileDigest(sourcePlaybook)
	if err != nil {
		return result, fmt.Errorf("digest playbook: %w", err)
	}
	// Refuse a queued job before creating a workspace or starting Ansible when
	// the allow-listed source no longer matches the immutable Run snapshot.
	// copyRegularTree's digest check below closes the subsequent copy-time
	// race, so mutated content is never executed.
	if req.ExpectedTreeSHA256 != "" && req.ExpectedTreeSHA256 != result.TreeSHA256 {
		return result, fmt.Errorf("%w: executable tree digest mismatch", ErrArtifactChanged)
	}
	if req.ExpectedPlaybookSHA256 != "" && req.ExpectedPlaybookSHA256 != result.PlaybookSHA256 {
		return result, fmt.Errorf("%w: playbook digest mismatch", ErrArtifactChanged)
	}

	workspace, err := r.createWorkspace()
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(workspace)

	jobRoot := filepath.Join(workspace, "job")
	if err := copyRegularTree(root, jobRoot); err != nil {
		return result, fmt.Errorf("snapshot playbook tree: %w", err)
	}
	lockedDigest, err := TreeDigest(jobRoot)
	if err != nil {
		return result, fmt.Errorf("digest workspace tree: %w", err)
	}
	if lockedDigest != result.TreeSHA256 {
		return result, errors.New("playbook tree changed while creating the run snapshot")
	}
	relPlaybook, err := filepath.Rel(root, sourcePlaybook)
	if err != nil {
		return result, fmt.Errorf("locate snapshotted playbook: %w", err)
	}
	playbook := filepath.Join(jobRoot, relPlaybook)

	inputDir := filepath.Join(workspace, "input")
	localTemp := filepath.Join(workspace, "ansible-local-tmp")
	if err := os.MkdirAll(inputDir, 0o700); err != nil {
		return result, fmt.Errorf("create input directory: %w", err)
	}
	if err := os.MkdirAll(localTemp, 0o700); err != nil {
		return result, fmt.Errorf("create Ansible local temp: %w", err)
	}
	inventoryPath := filepath.Join(inputDir, "inventory.ini")
	varsPath := filepath.Join(inputDir, "vars.json")
	if err := writePrivateFile(inventoryPath, req.Inventory); err != nil {
		return result, fmt.Errorf("write inventory: %w", err)
	}
	variables := req.Variables
	if variables == nil {
		variables = map[string]any{}
	}
	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return result, fmt.Errorf("encode variables: %w", err)
	}
	if err := writePrivateFile(varsPath, varsJSON); err != nil {
		return result, fmt.Errorf("write variables: %w", err)
	}

	redactor := NewRedactor(req.SecretValues, variables, req.Inventory)
	collector := newLogCollector(r.maxLogBytes(), req.LogSink, redactor)
	baseArgs := []string{"-i", inventoryPath, "--extra-vars", "@" + varsPath}
	if req.Limit != "" {
		baseArgs = append(baseArgs, "--limit", req.Limit)
	}
	if len(req.Tags) > 0 {
		baseArgs = append(baseArgs, "--tags", strings.Join(req.Tags, ","))
	}

	phases := []struct {
		phase Phase
		flag  string
	}{
		{phase: PhaseSyntaxCheck, flag: "--syntax-check"},
		{phase: PhaseListHosts, flag: "--list-hosts"},
		{phase: PhaseExecute},
	}
	for _, item := range phases {
		args := append([]string(nil), baseArgs...)
		if item.flag != "" {
			args = append(args, item.flag)
		}
		args = append(args, playbook)
		phaseResult, phaseErr := r.runPhase(ctx, item.phase, args, filepath.Dir(playbook), localTemp, collector)
		result.Phases = append(result.Phases, phaseResult)
		if phaseErr != nil {
			result.Logs, result.LogsTruncated = collector.Snapshot()
			result.Recap = collector.Recap()
			return result, &PhaseError{Phase: item.phase, ExitCode: phaseResult.ExitCode, Cause: phaseErr}
		}
	}
	result.Logs, result.LogsTruncated = collector.Snapshot()
	result.Recap = collector.Recap()
	return result, nil
}

func validateSelector(value, name string) error {
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: %s contains control characters", ErrInvalidRequest, name)
	}
	return nil
}

func (r *Runner) createWorkspace() (string, error) {
	root := r.WorkRoot
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create workspace root: %w", err)
	}
	workspace, err := os.MkdirTemp(root, "newplatform-ansible-")
	if err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", fmt.Errorf("secure workspace: %w", err)
	}
	return workspace, nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (r *Runner) runPhase(ctx context.Context, phase Phase, args []string, dir, localTemp string, logs *logCollector) (PhaseResult, error) {
	started := time.Now().UTC()
	phaseResult := PhaseResult{Phase: phase, StartedAt: started, ExitCode: -1}
	logs.Emit(phase, StreamSystem, "starting "+string(phase))

	binary := r.Binary
	if binary == "" {
		binary = "ansible-playbook"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = r.commandEnv(localTemp)
	configureProcessCancellation(cmd, r.killGrace())

	stdout := &lineWriter{phase: phase, stream: StreamStdout, logs: logs}
	stderr := &lineWriter{phase: phase, stream: StreamStderr, logs: logs}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return finishPhase(phaseResult, err, logs), err
	}
	waitErr := cmd.Wait()
	stdout.Flush()
	stderr.Flush()
	if ctx.Err() != nil {
		forceKillProcessGroup(cmd.Process)
	}

	if waitErr == nil {
		phaseResult.ExitCode = 0
		phaseResult.Successful = true
	} else {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			phaseResult.ExitCode = exitErr.ExitCode()
		}
		if ctx.Err() != nil {
			waitErr = ctx.Err()
		}
	}
	return finishPhase(phaseResult, waitErr, logs), waitErr
}

func finishPhase(result PhaseResult, err error, logs *logCollector) PhaseResult {
	result.Duration = time.Since(result.StartedAt)
	if err != nil {
		result.Error = logs.redactor.Redact(err.Error())
		logs.Emit(result.Phase, StreamSystem, "finished "+string(result.Phase)+": failed")
	} else {
		logs.Emit(result.Phase, StreamSystem, "finished "+string(result.Phase)+": successful")
	}
	return result
}

type lineWriter struct {
	mu      sync.Mutex
	pending []byte
	phase   Phase
	stream  Stream
	logs    *logCollector
}

func (w *lineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		w.emit(w.pending[:newline])
		w.pending = w.pending[newline+1:]
	}
	for len(w.pending) > maxScannerToken {
		w.emit(w.pending[:maxScannerToken])
		w.pending = w.pending[maxScannerToken:]
	}
	return len(data), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(w.pending)
		w.pending = nil
	}
}

func (w *lineWriter) emit(line []byte) {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	w.logs.Emit(w.phase, w.stream, string(line))
}

func (r *Runner) commandEnv(localTemp string) []string {
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			environment[key] = value
		}
	}
	for key, value := range r.Env {
		environment[key] = value
	}
	environment["ANSIBLE_LOCAL_TEMP"] = localTemp
	environment["ANSIBLE_RETRY_FILES_ENABLED"] = "False"
	environment["ANSIBLE_NOCOLOR"] = "True"
	environment["ANSIBLE_FORCE_COLOR"] = "0"
	environment["PYTHONUNBUFFERED"] = "1"
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

func (r *Runner) killGrace() time.Duration {
	if r.KillGrace <= 0 {
		return defaultKillGrace
	}
	return r.KillGrace
}

func (r *Runner) maxLogBytes() int {
	if r.MaxLogBytes <= 0 {
		return defaultMaxLogBytes
	}
	return r.MaxLogBytes
}

type logCollector struct {
	mu        sync.Mutex
	sinkMu    sync.Mutex
	maxBytes  int
	usedBytes int
	events    []LogEvent
	truncated bool
	sink      LogSink
	redactor  *Redactor
	recap     *recapParser
}

func newLogCollector(maxBytes int, sink LogSink, redactor *Redactor) *logCollector {
	return &logCollector{maxBytes: maxBytes, sink: sink, redactor: redactor, recap: newRecapParser()}
}

func (c *logCollector) Emit(phase Phase, stream Stream, line string) {
	event := LogEvent{Time: time.Now().UTC(), Phase: phase, Stream: stream, Line: c.redactor.Redact(line)}
	var sinkEvent *LogEvent
	c.mu.Lock()
	if phase == PhaseExecute {
		c.recap.Add(event.Line)
	}
	if c.usedBytes+len(event.Line) <= c.maxBytes {
		c.events = append(c.events, event)
		c.usedBytes += len(event.Line)
		sinkEvent = &event
	} else if !c.truncated {
		c.truncated = true
		marker := LogEvent{
			Time: event.Time, Phase: phase, Stream: StreamSystem,
			Line: fmt.Sprintf("[output truncated after %d bytes]", c.maxBytes),
		}
		sinkEvent = &marker
	}
	c.mu.Unlock()
	// The sink is the persistence/SSE path. Apply the same bound as the
	// in-memory result so a noisy playbook cannot bypass MaxLogBytes and grow
	// retained logs without limit. The first discarded line emits one marker.
	if c.sink != nil && sinkEvent != nil {
		c.sinkMu.Lock()
		func() {
			defer func() { _ = recover() }()
			c.sink(*sinkEvent)
		}()
		c.sinkMu.Unlock()
	}
}

func (c *logCollector) Snapshot() ([]LogEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := append([]LogEvent(nil), c.events...)
	return events, c.truncated
}

func (c *logCollector) Recap() map[string]HostRecap { return c.recap.Snapshot() }
