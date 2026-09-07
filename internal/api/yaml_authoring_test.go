package api

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
)

func TestWorkspaceUploadsRejectRemovedAndAmbiguousFields(t *testing.T) {
	for _, values := range []map[string][]string{
		{"confirmYamlMigration": {"true"}},
		{"gatherFacts": {"false"}},
		{"path": {"tasks/first.yml", "tasks/second.yml"}},
	} {
		r := &http.Request{MultipartForm: &multipart.Form{Value: values}}
		if err := validateWorkspaceUploadFields(r, "file", "path", "expectedSha256"); err == nil {
			t.Fatalf("unsupported upload fields accepted: %v", values)
		}
	}
	r := &http.Request{MultipartForm: &multipart.Form{Value: map[string][]string{"path": {"tasks/current.yml"}, "expectedSha256": {""}}}}
	if err := validateWorkspaceUploadFields(r, "file", "path", "expectedSha256"); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLAuthoringInputsRejectRemovedControls(t *testing.T) {
	for _, raw := range []string{`{"gatherFacts":false}`, `{"gatherFacts":true}`, `{"resourceContract":{"version":1,"noManagedPaths":true,"claims":[]}}`, `{"runtimeChecks":[]}`, `{"GatherFacts":true}`, `{"ResourceContract":{"Checks":[]},"resourceContract":{}}`} {
		var action actionSourceInput
		if err := json.Unmarshal([]byte(raw), &action); err == nil {
			t.Fatalf("removed action input accepted: %s", raw)
		}
		var job acceptanceJobInput
		if err := json.Unmarshal([]byte(raw), &job); err == nil {
			t.Fatalf("removed acceptance input accepted: %s", raw)
		}
	}
	var action actionSourceInput
	if err := json.Unmarshal([]byte(`{"kind":"rollback","preCheckActionId":"","postCheckActionId":"","become":false}`), &action); err != nil {
		t.Fatal(err)
	}
}
