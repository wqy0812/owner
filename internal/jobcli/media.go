package jobcli

import (
	"codex/platform-demo/internal/delivery"
	"context"
	"encoding/json"
	"errors"
)

func mediaPlan(metadata map[string]any) (delivery.Plan, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return delivery.Plan{}, err
	}
	var projection struct {
		delivery.Plan
		Steps []map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(encoded, &projection); err != nil {
		return delivery.Plan{}, err
	}
	if len(projection.Steps) == 0 {
		return delivery.Plan{}, errors.New("locked run plan contains no steps")
	}
	return projection.Plan, nil
}
func prepareJobMedia(ctx context.Context, metadata map[string]any) error {
	plan, err := mediaPlan(metadata)
	if err != nil {
		return err
	}
	return delivery.Prepare(ctx, plan, delivery.NewHTTPArtifactDelivery(nil), delivery.NewDockerImageDelivery("docker"))
}
