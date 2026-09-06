package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
		file, readErr := s.readReleasePlaybookFile(release, component, action.Playbook)
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
	decorated, err := s.releaseRules.decorateReleaseReadiness(ctx, release)
	if err != nil {
		return ReleaseReviewPreview{}, err
	}
	return ReleaseReviewPreview{
		ComponentID: component.ID, ComponentName: component.Name, OwnerID: owner.ID, OwnerName: owner.Name,
		Release: decorated, Playbooks: playbooks, PreviewDigest: hex.EncodeToString(digest[:]),
	}, nil
}

func (s *CatalogService) SubmitReleaseReview(ctx context.Context, user domain.User, id string) (domain.ComponentRelease, error) {
	release, err := s.store.GetComponentRelease(ctx, id)
	if err != nil {
		return release, err
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return release, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return release, err
	}
	if release.Status != domain.ReleaseDraft {
		return release, fmt.Errorf("%w: only a draft release can be submitted for review", domain.ErrConflict)
	}
	if err := s.releaseRules.validateReleaseContract(ctx, release, false); err != nil {
		return release, err
	}
	digest := componentReleaseSpecDigest(release)
	if err := s.store.SubmitComponentReleaseReview(ctx, id, digest, release.PublicationGeneration, time.Now().UTC()); err != nil {
		return release, err
	}
	s.audit.Record(ctx, user, "component_release.review_submitted", "component_release", id, map[string]any{"contractDigest": digest})
	s.hub.Publish("component_release.review_updated", map[string]any{"releaseId": id, "status": domain.ReleaseReviewPending})
	return s.GetComponentRelease(ctx, id)
}

func (s *CatalogService) DecideReleaseReview(ctx context.Context, user domain.User, id string, approve bool, comment, expectedPreviewDigest string) (domain.ComponentRelease, error) {
	if err := domain.ValidateRole(user, domain.RolePlatformAdmin); err != nil {
		return domain.ComponentRelease{}, err
	}
	expectedPreviewDigest = strings.TrimSpace(expectedPreviewDigest)
	if expectedPreviewDigest == "" {
		return domain.ComponentRelease{}, fmt.Errorf("%w: expectedPreviewDigest is required; preview the release before deciding", domain.ErrInvalid)
	}
	preview, err := s.releaseReviewPreview(ctx, id)
	if err != nil {
		return domain.ComponentRelease{}, err
	}
	if preview.PreviewDigest != expectedPreviewDigest {
		return preview.Release, fmt.Errorf("%w: release review preview changed; preview it again", domain.ErrConflict)
	}
	release := preview.Release
	status := domain.ReleaseReviewRejected
	if approve {
		status = domain.ReleaseReviewApproved
	}
	comment = strings.TrimSpace(comment)
	if !approve && comment == "" {
		return release, fmt.Errorf("%w: rejection comment is required", domain.ErrInvalid)
	}
	if err := s.store.DecideComponentReleaseReview(ctx, id, status, user.ID, comment, release.Review.ContractDigest, time.Now().UTC()); err != nil {
		return release, err
	}
	s.audit.Record(ctx, user, "component_release.review_decided", "component_release", id, map[string]any{"status": status, "comment": comment})
	s.hub.Publish("component_release.review_updated", map[string]any{"releaseId": id, "status": status})
	return s.GetComponentRelease(ctx, id)
}
