package service

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/imagebuild"
	"context"
	"errors"
	"testing"
	"time"
)

type buildStub struct {
	calls int
	err   error
}

func (b *buildStub) Build(context.Context, imagebuild.Request, func(imagebuild.LogEvent)) (imagebuild.Result, error) {
	b.calls++
	return imagebuild.Result{}, b.err
}
func TestImageBuildBackendKeepsFailureRecordsAndDoesNotPublish(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "interrupted"}[cancelled], func(t *testing.T) {
			platform, database := readinessTestPlatform(t)
			ctx := context.Background()
			now := time.Now().UTC()
			release := domain.ComponentRelease{ID: "build-release", ComponentID: "component-1", Version: "1.0.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
			if err := database.CreateComponentRelease(ctx, release); err != nil {
				t.Fatal(err)
			}
			backend := &buildStub{err: errors.New("docker failed")}
			platform.catalog.imageBuilder = backend
			// A build whose record could not transition to running must not execute.
			build := domain.ComponentImageBuild{ID: "build", ReleaseID: release.ID, RequestedBy: "component-owner", Status: domain.ImageBuildQueued, CreatedAt: now, ImageRef: "registry.test/a:1"}
			platform.catalog.executeComponentImageBuild(build, []byte("FROM scratch"))
			if backend.calls != 0 {
				t.Fatal("executed without running record")
			}
			if err := database.CreateComponentImageBuild(ctx, build); err != nil {
				t.Fatal(err)
			}
			want := domain.ImageBuildFailed
			if cancelled {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				platform.catalog.rootCtx = cancelCtx
				backend.err = context.Canceled
				want = domain.ImageBuildInterrupted
			}
			platform.catalog.executeComponentImageBuild(build, []byte("FROM scratch"))
			actual, err := database.GetComponentImageBuild(ctx, build.ID)
			if err != nil || actual.Status != want || actual.FinishedAt == nil || backend.calls != 1 {
				t.Fatalf("build=%+v err=%v calls=%d", actual, err, backend.calls)
			}
			if _, err := database.GetComponentImage(ctx, release.ID, "main"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("failed build registered an image: %v", err)
			}
		})
	}
}
