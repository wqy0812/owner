package service

import (
	"context"
	"fmt"
	"reflect"

	ansiblerunner "codex/platform-demo/internal/ansible"
)

// WorkspaceInspector provides the exact source checks needed by Catalog and
// planning. It does not execute jobs or discover optional runner capabilities.
type WorkspaceInspector interface {
	Digest(string) (string, string, error)
	DigestPlan([]string) (map[string]string, string, error)
	ValidatePlaybooks([]string) map[string]error
}

type RuntimeInspector interface {
	CheckRuntime(context.Context) error
	RuntimeIdentity(context.Context) (ansiblerunner.JobRuntime, error)
}

type JobBackend interface {
	BuildJob(context.Context, ansiblerunner.JobPlan) (*ansiblerunner.JobBundle, error)
	RunBundle(context.Context, *ansiblerunner.JobBundle, ansiblerunner.JobRequest) (ansiblerunner.JobResult, error)
}

type RunnerDependencies struct {
	Workspaces WorkspaceInspector
	Runtime    RuntimeInspector
	Jobs       JobBackend
}

func (r RunnerDependencies) validate() error {
	for _, dependency := range []struct {
		name  string
		value any
	}{{"workspaces", r.Workspaces}, {"runtime", r.Runtime}, {"jobs", r.Jobs}} {
		if dependency.value == nil || nilDependency(dependency.value) {
			return fmt.Errorf("runner dependency %s is required", dependency.name)
		}
	}
	return nil
}

func nilDependency(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

var (
	_ WorkspaceInspector = (*ansiblerunner.Runner)(nil)
	_ RuntimeInspector   = (*ansiblerunner.Runner)(nil)
	_ JobBackend         = (*ansiblerunner.Runner)(nil)
)
