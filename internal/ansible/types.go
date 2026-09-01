// Package ansible runs allow-listed Ansible playbooks in isolated workspaces.
package ansible

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Phase string

const (
	PhaseSyntaxCheck Phase = "syntax-check"
	PhaseListHosts   Phase = "list-hosts"
	PhaseExecute     Phase = "execute"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	StreamSystem Stream = "system"
)

type LogEvent struct {
	Time   time.Time `json:"time"`
	Phase  Phase     `json:"phase"`
	Stream Stream    `json:"stream"`
	Line   string    `json:"line"`
}

type LogSink func(LogEvent)

type Request struct {
	Playbook               string         `json:"playbook"`
	Inventory              []byte         `json:"-"`
	Variables              map[string]any `json:"variables,omitempty"`
	SecretValues           []string       `json:"-"`
	Limit                  string         `json:"limit,omitempty"`
	Tags                   []string       `json:"tags,omitempty"`
	Timeout                time.Duration  `json:"-"`
	ExpectedPlaybookSHA256 string         `json:"-"`
	ExpectedTreeSHA256     string         `json:"-"`
	LogSink                LogSink        `json:"-"`
}

type HostRecap struct {
	OK          int `json:"ok"`
	Changed     int `json:"changed"`
	Unreachable int `json:"unreachable"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Rescued     int `json:"rescued"`
	Ignored     int `json:"ignored"`
}

type PhaseResult struct {
	Phase      Phase         `json:"phase"`
	StartedAt  time.Time     `json:"startedAt"`
	Duration   time.Duration `json:"duration"`
	ExitCode   int           `json:"exitCode"`
	Successful bool          `json:"successful"`
	Error      string        `json:"error,omitempty"`
}

type Result struct {
	Playbook       string               `json:"playbook"`
	PlaybookSHA256 string               `json:"playbookSha256"`
	TreeSHA256     string               `json:"treeSha256"`
	StartedAt      time.Time            `json:"startedAt"`
	FinishedAt     time.Time            `json:"finishedAt"`
	Duration       time.Duration        `json:"duration"`
	Successful     bool                 `json:"successful"`
	Canceled       bool                 `json:"canceled"`
	TimedOut       bool                 `json:"timedOut"`
	Phases         []PhaseResult        `json:"phases"`
	Recap          map[string]HostRecap `json:"recap"`
	Logs           []LogEvent           `json:"logs"`
	LogsTruncated  bool                 `json:"logsTruncated"`
}

type Runner struct {
	AllowedRoot string
	WorkRoot    string
	Binary      string
	KillGrace   time.Duration
	MaxLogBytes int
	Env         map[string]string
}

func NewRunner(allowedRoot string) (*Runner, error) {
	r := &Runner{AllowedRoot: allowedRoot}
	if _, err := r.canonicalRoot(); err != nil {
		return nil, err
	}
	return r, nil
}

type PhaseError struct {
	Phase    Phase
	ExitCode int
	Cause    error
}

func (e *PhaseError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("ansible %s failed with exit code %d", e.Phase, e.ExitCode)
	}
	return fmt.Sprintf("ansible %s failed with exit code %d: %v", e.Phase, e.ExitCode, e.Cause)
}

func (e *PhaseError) Unwrap() error { return e.Cause }

var (
	ErrInvalidPath     = errors.New("invalid playbook path")
	ErrOutsideRoot     = errors.New("playbook is outside allowed root")
	ErrInvalidRequest  = errors.New("invalid ansible request")
	ErrArtifactChanged = errors.New("ansible artifact changed after the run was locked")
	ErrNoHostRecap     = errors.New("ansible execute phase completed without a host recap")
)

func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	return r.run(ctx, req)
}
