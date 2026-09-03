package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"codex/platform-demo/internal/domain"
)

type ReleaseReviewPlaybook struct {
	ActionID   string            `json:"actionId"`
	ActionName string            `json:"actionName"`
	ActionKind domain.ActionKind `json:"actionKind"`
	Path       string            `json:"path"`
	Filename   string            `json:"filename"`
	Content    string            `json:"content"`
	SHA256     string            `json:"sha256"`
}

type ReleaseReviewPreview struct {
	ComponentID   string                  `json:"componentId"`
	ComponentName string                  `json:"componentName"`
	OwnerID       string                  `json:"ownerId"`
	OwnerName     string                  `json:"ownerName"`
	Release       domain.ComponentRelease `json:"release"`
	Playbooks     []ReleaseReviewPlaybook `json:"playbooks"`
	PreviewDigest string                  `json:"previewDigest"`
}

func (s *CatalogService) PreviewReleaseReview(ctx context.Context, user domain.User, id string) (ReleaseReviewPreview, error) {
	if err := domain.ValidateRole(user, domain.RolePlatformAdmin); err != nil {
		return ReleaseReviewPreview{}, err
	}
	return s.releaseReviewPreview(ctx, id)
}

func (s *CatalogService) releaseReviewPreview(ctx context.Context, id string) (ReleaseReviewPreview, error) {
	release, err := s.store.GetComponentRelease(ctx, id)
	if err != nil {
		return ReleaseReviewPreview{}, err
	}
	if release.Status != domain.ReleaseDraft || release.Review.Status != domain.ReleaseReviewPending {
		return ReleaseReviewPreview{}, fmt.Errorf("%w: release review is no longer pending", domain.ErrConflict)
	}
	contractDigest := componentReleaseSpecDigest(release)
	if release.Review.ContractDigest == "" || release.Review.ContractDigest != contractDigest {
		return ReleaseReviewPreview{}, fmt.Errorf("%w: release review contract changed; submit it again", domain.ErrConflict)
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return ReleaseReviewPreview{}, err
	}
	owner, err := s.store.GetUser(ctx, component.OwnerID)
	if err != nil {
		return ReleaseReviewPreview{}, err
	}
	playbooks := make([]ReleaseReviewPlaybook, 0, len(release.Actions))
	for _, action := range release.Actions {
		file, readErr := s.platform.readReleasePlaybookFile(release, component, action.Playbook)
		if readErr != nil {
			return ReleaseReviewPreview{}, fmt.Errorf("%w: cannot preview %s playbook %q: %v", domain.ErrConflict, action.Kind, action.Playbook, readErr)
		}
		if action.PlaybookSHA256 == "" || file.SHA256 != action.PlaybookSHA256 {
			return ReleaseReviewPreview{}, fmt.Errorf("%w: %s playbook content changed; save the Draft and submit it again", domain.ErrConflict, action.Kind)
		}
		playbooks = append(playbooks, ReleaseReviewPlaybook{
			ActionID: action.ID, ActionName: action.Name, ActionKind: action.Kind,
			Path: file.Path, Filename: file.Filename, Content: file.Content, SHA256: file.SHA256,
		})
	}
	sort.SliceStable(playbooks, func(i, j int) bool {
		if playbooks[i].ActionID != playbooks[j].ActionID {
			return playbooks[i].ActionID < playbooks[j].ActionID
		}
		return playbooks[i].Path < playbooks[j].Path
	})
	digestInput := struct {
		ContractDigest string `json:"contractDigest"`
		Playbooks      []struct {
			ActionID string `json:"actionId"`
			Path     string `json:"path"`
			SHA256   string `json:"sha256"`
		} `json:"playbooks"`
	}{ContractDigest: contractDigest}
	for _, playbook := range playbooks {
		digestInput.Playbooks = append(digestInput.Playbooks, struct {
			ActionID string `json:"actionId"`
			Path     string `json:"path"`
			SHA256   string `json:"sha256"`
		}{ActionID: playbook.ActionID, Path: playbook.Path, SHA256: playbook.SHA256})
	}
	encoded, _ := json.Marshal(digestInput)
	digest := sha256.Sum256(encoded)
	decorated, err := s.platform.decorateReleaseReadiness(ctx, release)
	if err != nil {
		return ReleaseReviewPreview{}, err
	}
	return ReleaseReviewPreview{
		ComponentID: component.ID, ComponentName: component.Name, OwnerID: owner.ID, OwnerName: owner.Name,
		Release: decorated, Playbooks: playbooks, PreviewDigest: hex.EncodeToString(digest[:]),
	}, nil
}
