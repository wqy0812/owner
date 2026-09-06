package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/ansible"
)

// Runner is a test-only implementation of the explicit production ports. Its
// bundles pass the same manifest validation and persistence path as real jobs.
type Runner struct {
	DigestFunc   func(string) (string, string, error)
	PlanFunc     func([]string) (map[string]string, string, error)
	ValidateFunc func([]string) map[string]error
	RuntimeFunc  func(context.Context) (ansible.JobRuntime, error)
	BuildFunc    func(context.Context, ansible.JobPlan) (*ansible.JobBundle, error)
	RunFunc      func(context.Context, *ansible.JobBundle, ansible.JobRequest) (ansible.JobResult, error)
}

func (r *Runner) Digest(path string) (string, string, error) {
	if r.DigestFunc != nil {
		return r.DigestFunc(path)
	}
	return "", "", nil
}
func (r *Runner) DigestPlan(paths []string) (map[string]string, string, error) {
	if r.PlanFunc != nil {
		return r.PlanFunc(paths)
	}
	digests, tree := map[string]string{}, ""
	for _, path := range paths {
		digest, current, err := r.Digest(path)
		if err != nil {
			return nil, "", err
		}
		if tree != "" && tree != current {
			return nil, "", fmt.Errorf("test workspace trees differ")
		}
		digests[path], tree = digest, current
	}
	return digests, tree, nil
}
func (r *Runner) ValidatePlaybooks(paths []string) map[string]error {
	if r.ValidateFunc != nil {
		return r.ValidateFunc(paths)
	}
	result := map[string]error{}
	for _, path := range paths {
		_, _, result[path] = r.Digest(path)
	}
	return result
}
func (r *Runner) RuntimeIdentity(ctx context.Context) (ansible.JobRuntime, error) {
	if r.RuntimeFunc != nil {
		return r.RuntimeFunc(ctx)
	}
	return ansible.JobRuntime{}, nil
}
func (r *Runner) BuildJob(ctx context.Context, plan ansible.JobPlan) (*ansible.JobBundle, error) {
	if r.BuildFunc != nil {
		return r.BuildFunc(ctx, plan)
	}
	directory, err := os.MkdirTemp("", "clusterforge-test-bundle-")
	if err != nil {
		return nil, err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	manifest := ansible.JobManifest{Contract: ansible.JobContract, Plan: plan, Files: map[string]string{}, EntryPoint: "site.yml", FullPlanDigest: fmt.Sprintf("%x", sha256.Sum256(planJSON))}
	data, err := json.Marshal(manifest)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	manifest.Digest = fmt.Sprintf("%x", sha256.Sum256(data))
	data, err = json.Marshal(manifest)
	if err == nil {
		err = os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0600)
	}
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return &ansible.JobBundle{Path: directory, Manifest: manifest}, nil
}
func (r *Runner) RunBundle(ctx context.Context, bundle *ansible.JobBundle, request ansible.JobRequest) (ansible.JobResult, error) {
	if r.RunFunc != nil {
		return r.RunFunc(ctx, bundle, request)
	}
	return ansible.JobResult{}, fmt.Errorf("test runner has no execution callback")
}

// AdaptRunner preserves existing protocol-focused test doubles. Capability
// adaptation lives exclusively in test support, never in production execution.
func AdaptRunner(value any) *Runner {
	r := &Runner{}
	if v, ok := value.(interface {
		Digest(string) (string, string, error)
	}); ok {
		r.DigestFunc = v.Digest
	}
	if v, ok := value.(interface {
		DigestPlan([]string) (map[string]string, string, error)
	}); ok {
		r.PlanFunc = v.DigestPlan
	}
	if v, ok := value.(interface {
		ValidatePlaybooks([]string) map[string]error
	}); ok {
		r.ValidateFunc = v.ValidatePlaybooks
	}
	if v, ok := value.(interface {
		RuntimeIdentity(context.Context) (ansible.JobRuntime, error)
	}); ok {
		r.RuntimeFunc = v.RuntimeIdentity
	}
	if v, ok := value.(interface {
		RunJob(context.Context, ansible.JobRequest) (ansible.JobResult, error)
	}); ok {
		r.RunFunc = func(ctx context.Context, _ *ansible.JobBundle, request ansible.JobRequest) (ansible.JobResult, error) {
			return v.RunJob(ctx, request)
		}
	}
	if v, ok := value.(interface {
		BuildJob(context.Context, ansible.JobPlan) (*ansible.JobBundle, error)
		RunBundle(context.Context, *ansible.JobBundle, ansible.JobRequest) (ansible.JobResult, error)
	}); ok {
		r.BuildFunc, r.RunFunc = v.BuildJob, v.RunBundle
	}
	return r
}

func CompanionCLI(t testing.TB) {
	t.Helper()
	if os.Getenv("CLUSTERFORGE_JOB_CLI") != "" {
		return
	}
	path := filepath.Join(t.TempDir(), "clusterforge-job")
	if err := os.WriteFile(path, []byte("synthetic test companion; never executed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUSTERFORGE_JOB_CLI", path)
}

func (r *Runner) CheckRuntime(ctx context.Context) error {
	_, err := r.RuntimeIdentity(ctx)
	return err
}
