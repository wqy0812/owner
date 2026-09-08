package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestCleanupIdentityPreservesLocksWithoutDecodingExecutionControls(t *testing.T) {
	raw := []byte(`{"subject":{"componentReleaseSpecDigest":"component-digest","scenarioRevisionSpecDigest":"scenario-digest","componentTestEvidence":42},"plan":{"runtime":false,"steps":[{"releaseId":"release","releaseSpecDigest":"digest","timeoutSeconds":"broken"}],"parentSteps":[{"releaseId":"parent","releaseSpecDigest":"parent-digest"},{"releaseId":"release","releaseSpecDigest":"digest"},{"releaseId":"","releaseSpecDigest":""}]}}`)
	got, err := DecodeRunCleanupIdentity(raw)
	want := RunCleanupIdentity{ComponentReleaseSpecDigest: "component-digest", ScenarioRevisionSpecDigest: "scenario-digest", ReleaseLocks: []RunReleaseLock{{ReleaseID: "release", ReleaseSpecDigest: "digest"}, {ReleaseID: "parent", ReleaseSpecDigest: "parent-digest"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("retained identity: %+v %v", got, err)
	}
	// A failed record can legitimately have no planned steps.
	if got, err = DecodeRunCleanupIdentity([]byte(`{"subject":{},"plan":{"steps":null,"parentSteps":null}}`)); err != nil || len(got.ReleaseLocks) != 0 {
		t.Fatalf("empty plan identity: %+v %v", got, err)
	}
}

func TestCleanupIdentityRejectsDamagedOrAmbiguousIdentity(t *testing.T) {
	for name, raw := range map[string]string{
		"missing subject":    `{"plan":{"steps":[],"parentSteps":[]}}`,
		"missing plan":       `{"subject":{}}`,
		"missing steps":      `{"subject":{},"plan":{"parentSteps":[]}}`,
		"null digest":        `{"subject":{"componentReleaseSpecDigest":null},"plan":{"steps":[],"parentSteps":[]}}`,
		"wrong digest type":  `{"subject":{"scenarioRevisionSpecDigest":42},"plan":{"steps":[],"parentSteps":[]}}`,
		"wrong release type": `{"subject":{},"plan":{"steps":[{"releaseId":42,"releaseSpecDigest":"digest"}],"parentSteps":[]}}`,
		"missing lock":       `{"subject":{},"plan":{"steps":[{"releaseId":"release"}],"parentSteps":[]}}`,
		"null lock":          `{"subject":{},"plan":{"steps":[null],"parentSteps":[]}}`,
		"conflicting locks":  `{"subject":{},"plan":{"steps":[{"releaseId":"release","releaseSpecDigest":"digest"}],"parentSteps":[{"releaseId":"release","releaseSpecDigest":"other"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRunCleanupIdentity([]byte(raw)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("damaged identity accepted: %v", err)
			}
		})
	}
}
