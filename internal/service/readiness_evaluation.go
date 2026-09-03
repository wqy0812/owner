package service

import (
	"context"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

// A readinessEvaluation belongs to one synchronous read operation. Only the
// small governance dictionary and explicitly prepared file checks are reused;
// Run evidence and Release contracts are still checked live. Never retain it
// on Platform or carry it from a preview into a later mutation's validation.
type readinessEvaluation struct {
	platform      *Platform
	readCatalog   func(context.Context) (store.CatalogValidationSnapshot, error)
	catalog       store.CatalogValidationSnapshot
	catalogErr    error
	catalogLoaded bool
	playbooks     map[string]error
}

func newReadinessEvaluation(p *Platform) *readinessEvaluation {
	return &readinessEvaluation{platform: p, readCatalog: p.store.ReadCatalogDefinitions}
}

func (e *readinessEvaluation) catalogDefinitions(ctx context.Context) (store.CatalogValidationSnapshot, error) {
	if !e.catalogLoaded {
		e.catalog, e.catalogErr = e.readCatalog(ctx)
		e.catalogLoaded = true
	}
	return e.catalog, e.catalogErr
}

func (e *readinessEvaluation) readiness(ctx context.Context, release domain.ComponentRelease) (domain.ReleaseReadiness, error) {
	// Keep traversal state local to each root: cycle blockers depend on the
	// current dependency path and cannot be reused across independent roots.
	return e.releaseReadinessWithin(ctx, release, map[string]bool{}, map[string]domain.ReleaseReadiness{})
}

func (e *readinessEvaluation) decorateComponent(ctx context.Context, component domain.Component) (domain.Component, error) {
	if e.playbooks == nil {
		e.preparePlaybooks([]domain.Component{component})
	}
	for i := range component.Releases {
		readiness, err := e.readiness(ctx, component.Releases[i])
		if err != nil {
			return component, err
		}
		component.Releases[i].Readiness = readiness
	}
	return component, nil
}

// Prepare only for read-only list/detail decoration, never for a publication
// gate or execution plan. Missing entries (including candidate dependencies not
// visible in the response) fall back to an independent live check.
func (e *readinessEvaluation) preparePlaybooks(components []domain.Component) {
	validator, ok := e.platform.runner.(playbookValidationRunner)
	if !ok {
		return
	}
	var paths []string
	seen := map[string]bool{}
	for _, component := range components {
		for _, release := range component.Releases {
			for _, action := range release.Actions {
				if !seen[action.Playbook] {
					seen[action.Playbook] = true
					paths = append(paths, action.Playbook)
				}
			}
		}
	}
	e.playbooks = validator.ValidatePlaybooks(paths)
}

func (e *readinessEvaluation) validatePlaybook(path string) error {
	if err, found := e.playbooks[path]; found {
		return err
	}
	return e.platform.validatePlaybook(path)
}
