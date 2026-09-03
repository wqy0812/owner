package service

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

func isSensitiveKey(key string) bool { return domain.IsSensitiveKey(key) }

// ResolveParameters applies the three owner-controlled layers. Upstream
// mappings are applied separately after the upstream node has completed.
func ResolveParameters(componentFixed, scenarioValues, environmentValues map[string]any) map[string]any {
	resolved := make(map[string]any)
	mergeMap(resolved, componentFixed)
	mergeMap(resolved, scenarioValues)
	mergeMap(resolved, environmentValues)
	return resolved
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			base, _ := dst[key].(map[string]any)
			if base == nil {
				base = make(map[string]any)
			}
			mergeMap(base, child)
			dst[key] = base
			continue
		}
		dst[key] = deepCopy(value)
	}
}

func deepCopy(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		mergeMap(out, typed)
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = deepCopy(typed[i])
		}
		return out
	default:
		return typed
	}
}

// ValidateCredentialRefs ensures that persisted credentials are references,
// never inline values. Environment owners may use an SSH key path or an env
// var reference only.
func ValidateCredentialRefs(refs []domain.CredentialRef) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || !ref.Valid() {
			return fmt.Errorf("%w: credential reference requires a name and kind sshKeyPath or envVarRef", domain.ErrInvalid)
		}
		if _, ok := seen[ref.Name]; ok {
			return fmt.Errorf("%w: duplicate credential reference %q", domain.ErrInvalid, ref.Name)
		}
		seen[ref.Name] = struct{}{}
		if strings.TrimSpace(ref.Reference) == "" {
			return fmt.Errorf("%w: credential reference %q is empty", domain.ErrInvalid, ref.Name)
		}
		if ref.Kind == "envVarRef" {
			for _, runeValue := range ref.Reference {
				if !(runeValue == '_' || runeValue >= 'A' && runeValue <= 'Z' || runeValue >= '0' && runeValue <= '9') {
					return fmt.Errorf("%w: envVarRef %q must be an uppercase environment variable name", domain.ErrInvalid, ref.Name)
				}
			}
		}
		if ref.Kind == "sshKeyPath" && !strings.HasPrefix(ref.Reference, "/") {
			return fmt.Errorf("%w: sshKeyPath %q must be absolute", domain.ErrInvalid, ref.Name)
		}
	}
	return nil
}

// Redact recursively produces a copy safe for API responses, audit metadata,
// run snapshots and retained logs.
func Redact(value any, literalSecrets ...string) any {
	secretSet := make([]string, 0, len(literalSecrets))
	for _, item := range literalSecrets {
		if item != "" {
			secretSet = append(secretSet, item)
		}
	}
	return redactValue(value, secretSet, "")
}

func redactValue(value any, secrets []string, key string) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactValue(child, secrets, childKey)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = redactValue(typed[i], secrets, key)
		}
		return out
	case string:
		out := typed
		for _, secret := range secrets {
			out = strings.ReplaceAll(out, secret, "[REDACTED]")
		}
		return out
	default:
		return typed
	}
}

// BuildImpactReport walks the reverse dependency graph. A path begins with
// the published component and ends at each directly or transitively affected
// downstream component. Owners and scenario references are de-duplicated.
func BuildImpactReport(
	publishedComponentID string,
	components []domain.Component,
	releases []domain.ComponentRelease,
	scenarios []domain.Scenario,
) domain.ImpactReport {
	componentByID := make(map[string]domain.Component, len(components))
	for _, component := range components {
		componentByID[component.ID] = component
	}

	reverse := map[string][]string{}
	for _, release := range releases {
		for _, dependency := range release.Dependencies {
			reverse[dependency.UpstreamComponentID] = appendUnique(reverse[dependency.UpstreamComponentID], release.ComponentID)
		}
	}
	for id := range reverse {
		sort.Strings(reverse[id])
	}

	type queueItem struct {
		id   string
		path []string
	}
	queue := []queueItem{{id: publishedComponentID, path: []string{publishedComponentID}}}
	paths := map[string][][]string{}
	seenPath := map[string]struct{}{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, child := range reverse[item.id] {
			if contains(item.path, child) { // cycle protection without losing alternate DAG paths
				continue
			}
			nextPath := append(append([]string(nil), item.path...), child)
			key := strings.Join(nextPath, "\x00")
			if _, ok := seenPath[key]; ok {
				continue
			}
			seenPath[key] = struct{}{}
			paths[child] = append(paths[child], nextPath)
			queue = append(queue, queueItem{id: child, path: nextPath})
		}
	}

	recipients := map[string]*domain.ImpactRecipient{}
	for componentID, componentPaths := range paths {
		component, ok := componentByID[componentID]
		if !ok || component.OwnerID == "" {
			continue
		}
		recipient := ensureRecipient(recipients, component.OwnerID, domain.RoleComponentOwner)
		for _, path := range componentPaths {
			recipient.Paths = appendImpactPath(recipient.Paths, namedPath(path, componentByID))
		}
	}

	affectedComponents := map[string]struct{}{publishedComponentID: {}}
	for id := range paths {
		affectedComponents[id] = struct{}{}
	}
	for _, scenario := range scenarios {
		matchedComponents := map[string]bool{}
		for _, revision := range scenario.Revisions {
			for _, node := range revision.Graph.Nodes {
				releaseComponentID := ""
				for _, release := range releases {
					if release.ID == node.ReleaseID {
						releaseComponentID = release.ComponentID
						break
					}
				}
				if _, ok := affectedComponents[releaseComponentID]; ok {
					matchedComponents[releaseComponentID] = true
				}
			}
		}
		if len(matchedComponents) > 0 {
			recipient := ensureRecipient(recipients, scenario.OwnerID, domain.RoleScenarioOwner)
			recipient.ScenarioIDs = appendUnique(recipient.ScenarioIDs, scenario.ID)
			for componentID := range matchedComponents {
				componentPaths := paths[componentID]
				if componentID == publishedComponentID {
					componentPaths = [][]string{{publishedComponentID}}
				}
				for _, path := range componentPaths {
					recipient.Paths = appendImpactPath(recipient.Paths, namedPath(path, componentByID))
				}
			}
		}
	}

	keys := make([]string, 0, len(recipients))
	for id := range recipients {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	report := domain.ImpactReport{ComponentID: publishedComponentID, Recipients: make([]domain.ImpactRecipient, 0, len(keys))}
	for _, id := range keys {
		recipient := recipients[id]
		sort.Strings(recipient.ScenarioIDs)
		report.Recipients = append(report.Recipients, *recipient)
	}
	return report
}

// BuildReleaseImpactReport follows exact Release locks. Publishing an
// evolution only affects consumers that lock its parent Release; unrelated
// release lines of the same component are intentionally excluded.
func BuildReleaseImpactReport(
	startReleaseID string,
	components []domain.Component,
	releases []domain.ComponentRelease,
	scenarios []domain.Scenario,
) domain.ImpactReport {
	componentByID := make(map[string]domain.Component, len(components))
	releaseByID := make(map[string]domain.ComponentRelease, len(releases))
	for _, component := range components {
		componentByID[component.ID] = component
	}
	for _, release := range releases {
		releaseByID[release.ID] = release
	}
	start, ok := releaseByID[startReleaseID]
	if !ok {
		return domain.ImpactReport{Recipients: []domain.ImpactRecipient{}}
	}
	reverse := map[string][]string{}
	for _, release := range releases {
		for _, dependency := range release.Dependencies {
			reverse[dependency.UpstreamReleaseID] = appendUnique(reverse[dependency.UpstreamReleaseID], release.ID)
		}
	}
	type queueItem struct {
		releaseID string
		path      []string
	}
	queue := []queueItem{{releaseID: startReleaseID, path: []string{start.ComponentID}}}
	affected := map[string]bool{startReleaseID: true}
	recipients := map[string]*domain.ImpactRecipient{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, downstreamID := range reverse[item.releaseID] {
			downstream, exists := releaseByID[downstreamID]
			if !exists {
				continue
			}
			nextPath := append(append([]string(nil), item.path...), downstream.ComponentID)
			component := componentByID[downstream.ComponentID]
			if component.OwnerID != "" {
				recipient := ensureRecipient(recipients, component.OwnerID, domain.RoleComponentOwner)
				recipient.Paths = appendImpactPath(recipient.Paths, namedPath(nextPath, componentByID))
			}
			if !affected[downstreamID] {
				affected[downstreamID] = true
				queue = append(queue, queueItem{releaseID: downstreamID, path: nextPath})
			}
		}
	}
	for _, scenario := range scenarios {
		matched := false
		for _, revision := range scenario.Revisions {
			for _, node := range revision.Graph.Nodes {
				if affected[node.ReleaseID] {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched && scenario.OwnerID != "" {
			recipient := ensureRecipient(recipients, scenario.OwnerID, domain.RoleScenarioOwner)
			recipient.ScenarioIDs = appendUnique(recipient.ScenarioIDs, scenario.ID)
		}
	}
	result := domain.ImpactReport{ComponentID: start.ComponentID, Recipients: []domain.ImpactRecipient{}}
	for _, recipient := range recipients {
		result.Recipients = append(result.Recipients, *recipient)
	}
	sort.Slice(result.Recipients, func(i, j int) bool {
		if result.Recipients[i].Role != result.Recipients[j].Role {
			return result.Recipients[i].Role < result.Recipients[j].Role
		}
		return result.Recipients[i].UserID < result.Recipients[j].UserID
	})
	return result
}

func ensureRecipient(values map[string]*domain.ImpactRecipient, userID string, role domain.Role) *domain.ImpactRecipient {
	key := string(role) + ":" + userID
	if values[key] == nil {
		values[key] = &domain.ImpactRecipient{UserID: userID, Role: role}
	}
	return values[key]
}

func appendUnique(values []string, value string) []string {
	if !contains(values, value) {
		return append(values, value)
	}
	return values
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func namedPath(ids []string, components map[string]domain.Component) domain.ImpactPath {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = components[id].Name
		if names[i] == "" {
			names[i] = id
		}
	}
	return domain.ImpactPath{ComponentIDs: append([]string(nil), ids...), ComponentNames: names}
}

func appendImpactPath(paths []domain.ImpactPath, path domain.ImpactPath) []domain.ImpactPath {
	key := strings.Join(path.ComponentIDs, "\x00")
	for _, existing := range paths {
		if strings.Join(existing.ComponentIDs, "\x00") == key {
			return paths
		}
	}
	return append(paths, path)
}
