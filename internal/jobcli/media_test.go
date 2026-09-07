package jobcli

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMediaProjectionPreservesFullMetadata(t *testing.T) {
	metadata := map[string]any{"steps": []any{map[string]any{"id": "step-1", "variables": map[string]any{"secretRef": "ref"}}}, "unrelated": map[string]any{"version": 7}, "deliveryRequirements": []any{map[string]any{"id": "a", "kind": "artifact", "identity": "sha256:abc"}}, "artifactTransfers": []any{map[string]any{"requirementId": "a", "sourceUrl": "https://source/a", "sha256": "abc"}}}
	before, _ := json.Marshal(metadata)
	projection, err := mediaPlan(metadata)
	if err != nil || len(projection.DeliveryRequirements) != 1 || projection.ArtifactTransfers[0].SourceURL != "https://source/a" {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	after, _ := json.Marshal(metadata)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("projection changed original metadata")
	}
	if _, err := mediaPlan(map[string]any{"steps": []any{map[string]any{"id": "step"}}}); err != nil {
		t.Fatal(err)
	}
}
func TestMediaProjectionRejectsMalformedMetadata(t *testing.T) {
	for _, metadata := range []map[string]any{nil, {}, {"steps": []any{}}, {"steps": "bad"}, {"steps": []any{1}}, {"steps": []any{map[string]any{}}, "artifactTransfers": "bad"}, {"steps": []any{map[string]any{}}, "deliveryRequirements": []any{map[string]any{"sizeBytes": "bad"}}}, {"steps": []any{map[string]any{}}, "invalid": make(chan int)}} {
		if _, err := mediaPlan(metadata); err == nil {
			t.Fatalf("accepted malformed metadata: %v", metadata)
		}
	}
}
