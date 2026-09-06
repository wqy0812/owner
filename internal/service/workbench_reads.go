package service

import (
	"context"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

// Memoization belongs to this response only. Mutating gates use their own live
// reads and cannot inherit a workbench's evidence or filesystem results.
type workbenchReadStore struct {
	readModelStore
	releases    map[string]domain.ComponentRelease
	revisions   map[string]domain.ScenarioRevision
	failedNodes map[string]string
}

func newWorkbenchReadStore(s readModelStore) *workbenchReadStore {
	return &workbenchReadStore{readModelStore: s, releases: map[string]domain.ComponentRelease{}, revisions: map[string]domain.ScenarioRevision{}, failedNodes: map[string]string{}}
}

func (s *workbenchReadStore) WorkbenchSubjects(ctx context.Context, user domain.User) (store.WorkbenchSubjects, error) {
	out, err := s.readModelStore.WorkbenchSubjects(ctx, user)
	for _, c := range out.Components {
		for _, r := range c.Releases {
			s.releases[r.ID] = r
		}
	}
	for _, sc := range out.Scenarios {
		for _, r := range sc.Revisions {
			s.revisions[r.ID] = r
		}
	}
	return out, err
}

func (s *workbenchReadStore) prefetch(ctx context.Context, runs []domain.Run) error {
	releases, revisions := map[string]bool{}, map[string]bool{}
	for _, run := range runs {
		if id := run.ComponentReleaseID; id != "" {
			if _, ok := s.releases[id]; !ok {
				releases[id] = true
			}
		}
		if id := run.ScenarioRevisionID; id != "" {
			if _, ok := s.revisions[id]; !ok {
				revisions[id] = true
			}
		}
		steps, _ := run.InputSnapshot["steps"].([]any)
		for _, step := range steps {
			locked, _ := step.(map[string]any)
			if id, ok := locked["releaseId"].(string); ok && id != "" {
				if _, exists := s.releases[id]; !exists {
					releases[id] = true
				}
			}
		}
	}
	if len(releases)+len(revisions) == 0 {
		return nil
	}
	contracts, err := s.readModelStore.WorkbenchContracts(ctx, releases, revisions)
	if err != nil {
		return err
	}
	for _, r := range contracts.Releases {
		s.releases[r.ID] = r
	}
	for _, r := range contracts.Revisions {
		s.revisions[r.ID] = r
	}
	return nil
}

func (s *workbenchReadStore) WorkbenchRuns(ctx context.Context, user domain.User) ([]domain.Run, error) {
	runs, err := s.readModelStore.WorkbenchRuns(ctx, user)
	if err != nil {
		return nil, err
	}
	failed := []domain.Run{}
	ids := map[string]bool{}
	for _, r := range runs {
		if (r.Status == domain.RunFailed || r.Status == domain.RunInterrupted) && r.InputSnapshot["cleaned"] != true {
			failed = append(failed, r)
			ids[r.ID] = true
		}
	}
	if err = s.prefetch(ctx, failed); err != nil {
		return nil, err
	}
	if user.Role == domain.RoleComponentOwner && len(ids) > 0 {
		s.failedNodes, err = s.readModelStore.WorkbenchFailedNodes(ctx, ids)
	}
	return runs, err
}

func (s *workbenchReadStore) WorkbenchScenarioRuns(ctx context.Context, user domain.User, id, digest string, success bool, before time.Time, beforeID string) ([]domain.Run, error) {
	runs, err := s.readModelStore.WorkbenchScenarioRuns(ctx, user, id, digest, success, before, beforeID)
	if err == nil {
		err = s.prefetch(ctx, runs)
	}
	return runs, err
}

func (s *workbenchReadStore) ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error) {
	if node, ok := s.failedNodes[runID]; ok {
		if node == "" {
			return nil, nil
		}
		return []domain.RunStep{{NodeID: node, Status: domain.RunFailed}}, nil
	}
	return s.readModelStore.ListRunSteps(ctx, runID)
}
func (s *workbenchReadStore) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	if r, ok := s.releases[id]; ok {
		return r, nil
	}
	r, err := s.readModelStore.GetComponentRelease(ctx, id)
	if err == nil {
		s.releases[id] = r
	}
	return r, err
}
func (s *workbenchReadStore) GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error) {
	if r, ok := s.revisions[id]; ok {
		return r, nil
	}
	r, err := s.readModelStore.GetScenarioRevision(ctx, id)
	if err == nil {
		s.revisions[id] = r
	}
	return r, err
}

func (p *ReadModelService) workbenchRuns(ctx context.Context, user domain.User, components []domain.Component, scenarios []domain.Scenario) ([]domain.Run, error) {
	runs, err := p.store.WorkbenchRuns(ctx, user)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	add := func(items []domain.Run) {
		for _, r := range items {
			byID[r.ID] = r
		}
	}
	for _, c := range components {
		for _, r := range c.Releases {
			matches, e := p.store.WorkbenchComponentRun(ctx, user, r.ID, componentReleaseSpecDigest(r))
			if e != nil {
				return nil, e
			}
			add(matches)
		}
	}
	for _, sc := range scenarios {
		revision, ok := currentScenarioRevision(sc)
		if !ok {
			continue
		}
		// Latest test of any definition is needed for the stale-evidence reason.
		lastSuccess, e := p.store.WorkbenchScenarioRuns(ctx, user, revision.ID, "", true, time.Time{}, "")
		if e != nil {
			return nil, e
		}
		if len(lastSuccess) > 0 {
			add(lastSuccess[:1])
		}
		var before time.Time
		beforeID := ""
		foundCurrent, foundSuccess := false, false
		for !foundCurrent || !foundSuccess {
			page, e := p.store.WorkbenchScenarioRuns(ctx, user, revision.ID, scenarioRevisionSpecDigest(revision), foundCurrent, before, beforeID)
			if e != nil {
				return nil, e
			}
			if len(page) == 0 {
				break
			}
			for _, run := range page {
				matches, e := scenarioRunDefinitionCurrent(ctx, run, revision, p.store.GetComponentRelease)
				if e != nil {
					return nil, e
				}
				if matches {
					if !foundCurrent {
						add([]domain.Run{run})
						foundCurrent = true
					}
					if run.Status == domain.RunSucceeded {
						add([]domain.Run{run})
						foundSuccess = true
						break
					}
				}
			}
			last := page[len(page)-1]
			before, beforeID = last.CreatedAt, last.ID
		}
	}
	out := make([]domain.Run, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
