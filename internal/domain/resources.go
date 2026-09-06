package domain

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const ResourceContractVersion = 1

// A nil contract is historical/undeclared. An explicit empty declaration must
// set NoManagedPaths; migrations must never manufacture author consent.
type ResourceContract struct {
	Checks         []RuntimeCheck  `json:"checks,omitempty"`
	Version        int             `json:"version"`
	NoManagedPaths bool            `json:"noManagedPaths"`
	Claims         []ResourceClaim `json:"claims"`
}
type ResourceClaim struct {
	ID          string             `json:"id"`
	Path        string             `json:"path"`
	Scope       string             `json:"scope"`  // file or tree
	Access      string             `json:"access"` // manage, read, verify
	Exclusive   bool               `json:"exclusive,omitempty"`
	Excludes    []string           `json:"excludes,omitempty"`
	SharedPaths []string           `json:"sharedPaths,omitempty"`
	SharedWith  *ResourceReference `json:"sharedWith,omitempty"`
}
type ResourceReference struct {
	ReleaseID string `json:"releaseId"`
	ClaimID   string `json:"claimId"`
}
type ResourceInstance struct {
	OwnerID   string        `json:"ownerId"`
	ReleaseID string        `json:"releaseId"`
	ActionID  string        `json:"actionId"`
	HostGroup string        `json:"hostGroup"`
	Hosts     []string      `json:"hosts,omitempty"`
	Claim     ResourceClaim `json:"claim"`
}

var resourceParameter = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
var resourceID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ResourcePathParameters(value string) []string {
	out := []string{}
	for _, m := range resourceParameter.FindAllStringSubmatch(value, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		out = append(out, name)
	}
	return out
}
func validateResourcePath(value string) error {
	resolved := resourceParameter.ReplaceAllString(value, "parameter")
	if loc := resourceParameter.FindStringIndex(value); loc != nil && loc[0] == 0 {
		resolved = "/" + resolved
	}
	if !strings.HasPrefix(resolved, "/") || strings.ContainsAny(resolved, "\x00\\*?[]{}$") || path.Clean(resolved) != resolved || resolved == "/" {
		return fmt.Errorf("%w: resource path must be a normalized absolute file/directory path: %q", ErrInvalid, value)
	}
	return nil
}
func ValidateResourceContract(contract *ResourceContract, parameters []ParameterDefinition, required bool) error {
	if contract == nil {
		if required {
			return &CodedError{Code: "resource.contract_missing", Message: "动作尚未声明资源管理范围，请由组件 Owner 补齐合同", Cause: ErrConflict}
		}
		return nil
	}
	if contract.Version != ResourceContractVersion {
		return fmt.Errorf("%w: unsupported resource contract version", ErrInvalid)
	}
	if contract.NoManagedPaths == (len(contract.Claims) > 0) {
		return fmt.Errorf("%w: choose resource claims or explicitly confirm no managed paths", ErrInvalid)
	}
	names := map[string]bool{}
	for _, p := range parameters {
		if p.Type == "string" {
			names[p.Name] = true
		}
	}

	checkIDs := map[string]bool{}
	for _, check := range contract.Checks {
		if !resourceID.MatchString(check.ID) || checkIDs[check.ID] {
			return fmt.Errorf("%w: runtime check ID must be unique", ErrInvalid)
		}
		checkIDs[check.ID] = true
		switch check.Kind {
		case "command", "image_command", "path_present", "path_absent", "service_inactive", "network_rules_absent", "tcp":
		default:
			return fmt.Errorf("%w: unsupported read-only runtime check", ErrInvalid)
		}
		if strings.TrimSpace(check.Target) == "" {
			return fmt.Errorf("%w: runtime check target is required", ErrInvalid)
		}
		for _, name := range ResourcePathParameters(check.Target) {
			if !names[name] {
				return fmt.Errorf("%w: runtime check parameter %s is not a governed string", ErrInvalid, name)
			}
		}
	}
	ids := map[string]bool{}
	for _, claim := range contract.Claims {
		if !resourceID.MatchString(claim.ID) || ids[claim.ID] {
			return fmt.Errorf("%w: resource claim ID must be unique and nonempty", ErrInvalid)
		}
		ids[claim.ID] = true
		if claim.Scope != "file" && claim.Scope != "tree" {
			return fmt.Errorf("%w: resource scope must be file or tree", ErrInvalid)
		}
		if claim.Access != "manage" && claim.Access != "read" && claim.Access != "verify" {
			return fmt.Errorf("%w: resource access must be manage, read or verify", ErrInvalid)
		}
		if claim.Scope != "tree" && (len(claim.Excludes) > 0 || len(claim.SharedPaths) > 0) {
			return fmt.Errorf("%w: only directory trees may exclude or share subpaths", ErrInvalid)
		}
		if claim.Exclusive && (len(claim.SharedPaths) > 0 || len(claim.Excludes) > 0) {
			return fmt.Errorf("%w: exclusive whole-tree validation cannot exclude or share paths", ErrInvalid)
		}
		for _, value := range append(append([]string{claim.Path}, claim.Excludes...), claim.SharedPaths...) {
			if err := validateResourcePath(value); err != nil {
				return err
			}
			for _, name := range ResourcePathParameters(value) {
				if !names[name] {
					return fmt.Errorf("%w: resource path parameter %s must be a governed non-secret string", ErrInvalid, name)
				}
			}
		}
		if claim.SharedWith != nil && (claim.SharedWith.ReleaseID == "" || claim.SharedWith.ClaimID == "") {
			return fmt.Errorf("%w: shared resource requires an exact upstream release and claim", ErrInvalid)
		}
	}
	return nil
}
func ResolveResourceContract(contract *ResourceContract, values map[string]any) ([]ResourceClaim, error) {
	if contract == nil {
		return nil, &CodedError{Code: "resource.contract_missing", Message: "动作尚未声明资源管理范围", Cause: ErrConflict}
	}
	resolve := func(value string) (string, error) {
		var failure error
		result := resourceParameter.ReplaceAllStringFunc(value, func(token string) string {
			name := ResourcePathParameters(token)[0]
			v, ok := values[name].(string)
			if !ok || v == "" {
				failure = fmt.Errorf("%w: resource path parameter %s is unresolved", ErrConflict, name)
			}
			return v
		})
		if failure != nil {
			return "", failure
		}
		if len(ResourcePathParameters(result)) > 0 {
			return "", fmt.Errorf("%w: resolved resource contains another unresolved parameter", ErrConflict)
		}
		if err := validateResourcePath(result); err != nil {
			return "", err
		}
		return result, nil
	}
	out := []ResourceClaim{}
	for _, original := range contract.Claims {
		claim := original
		var err error
		if claim.Path, err = resolve(claim.Path); err != nil {
			return nil, err
		}
		lists := [][]string{original.Excludes, original.SharedPaths}
		resolved := make([][]string, 2)
		for i, list := range lists {
			for _, value := range list {
				v, e := resolve(value)
				if e != nil {
					return nil, e
				}
				if !resourceChild(claim.Path, v) || v == claim.Path {
					return nil, fmt.Errorf("%w: excluded/shared paths must be strict descendants of %s", ErrInvalid, claim.Path)
				}
				resolved[i] = append(resolved[i], v)
			}
		}
		claim.Excludes, claim.SharedPaths = resolved[0], resolved[1]
		out = append(out, claim)
	}
	return out, nil
}
func resourceChild(parent, child string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/")
}
func ResourceHostsOverlap(a, b ResourceInstance) bool {
	if a.Hosts == nil || b.Hosts == nil {
		return a.HostGroup == b.HostGroup || a.HostGroup == "all" || b.HostGroup == "all"
	}
	for _, x := range a.Hosts {
		for _, y := range b.Hosts {
			if x == y {
				return true
			}
		}
	}
	return false
}
func resourceExcluded(c ResourceClaim, value string) bool {
	for _, excluded := range c.Excludes {
		if resourceChild(excluded, value) {
			return true
		}
	}
	return false
}
func ResourceClaimsOverlap(a, b ResourceClaim) bool {
	if a.Path == b.Path {
		return true
	}
	if a.Scope == "tree" && resourceChild(a.Path, b.Path) {
		return !resourceExcluded(a, b.Path)
	}
	if b.Scope == "tree" && resourceChild(b.Path, a.Path) {
		return !resourceExcluded(b, a.Path)
	}
	return false
}

// Validation examines authored ownership. It does not infer arbitrary script
// side effects or turn ordering into permission for another writer.
func ResourceConflicts(instances []ResourceInstance) []ValidationIssue {
	issues := []ValidationIssue{}
	for i, a := range instances {
		if ref := a.Claim.SharedWith; ref != nil {
			found := false
			for _, b := range instances {
				if ResourceHostsOverlap(a, b) && resourceSharingAllows(a, b) {
					found = true
				}
			}

			if found && a.Hosts != nil {
				for _, host := range a.Hosts {
					covered := false
					for _, b := range instances {
						if !resourceSharingAllows(a, b) {
							continue
						}
						for _, providerHost := range b.Hosts {
							if providerHost == host {
								covered = true
							}
						}
					}
					if !covered {
						found = false
					}
				}
			}
			if !found {
				issues = append(issues, ValidationIssue{Code: "resource.shared_source_invalid", NodeID: a.OwnerID, Message: "共享路径没有匹配的上游资源声明：" + a.Claim.Path})
			}
		}
		for _, b := range instances[i+1:] {
			if a.OwnerID == b.OwnerID && a.ReleaseID == b.ReleaseID {
				continue
			}
			if !ResourceHostsOverlap(a, b) || !ResourceClaimsOverlap(a.Claim, b.Claim) {
				continue
			}

			shared := resourceSharingAllows(a, b) || resourceSharingAllows(b, a)
			if shared && !a.Claim.Exclusive && !b.Claim.Exclusive {
				continue
			}
			if a.Claim.Access == "read" && b.Claim.Access == "manage" || b.Claim.Access == "read" && a.Claim.Access == "manage" {
				issues = append(issues, ValidationIssue{Code: "resource.read_source_unresolved", NodeID: a.OwnerID, Message: "只读消费必须绑定提供该路径的精确上游资源声明：" + a.Claim.Path})
			}
			writes := a.Claim.Access == "manage" && b.Claim.Access == "manage"
			exclusive := a.Claim.Exclusive && b.Claim.Access == "manage" || b.Claim.Exclusive && a.Claim.Access == "manage"
			if writes || exclusive {
				issues = append(issues, ValidationIssue{Code: "resource.path_conflict", NodeID: a.OwnerID, Message: fmt.Sprintf("资源范围冲突：%s (%s) 与 %s (%s)；请调整组件合同中的路径分工", a.Claim.Path, a.ReleaseID, b.Claim.Path, b.ReleaseID)})
			}
		}
	}
	return issues
}

// RuntimeCheck selects an allowlisted read-only probe, never a shell program.
type RuntimeCheck struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Target              string `json:"target"`
	ProvidedByReleaseID string `json:"providedByReleaseId,omitempty"`
}

// A consumer must stay entirely inside its provider's declaration, including
// exclusions. Merely intersecting a file or subtree does not provide its parent.
func resourceReadCovered(consumer, provider ResourceClaim) bool {
	if !resourceChild(provider.Path, consumer.Path) || resourceExcluded(provider, consumer.Path) {
		return false
	}
	if provider.Scope == "file" {
		return consumer.Scope == "file" && consumer.Path == provider.Path
	}
	if consumer.Scope == "tree" {
		for _, excluded := range provider.Excludes {
			if resourceChild(consumer.Path, excluded) && !resourceExcluded(consumer, excluded) {
				return false
			}
		}
	}
	return true
}

func resourceSharingAllows(child, parent ResourceInstance) bool {
	ref := child.Claim.SharedWith
	if ref == nil || ref.ReleaseID != parent.ReleaseID || ref.ClaimID != parent.Claim.ID {
		return false
	}
	if child.Claim.Access == "read" {
		return resourceReadCovered(child.Claim, parent.Claim)
	}
	if child.Claim.Path == parent.Claim.Path {
		return false
	}
	for _, shared := range parent.Claim.SharedPaths {
		if resourceChild(shared, child.Claim.Path) {
			return true
		}
	}
	return false
}
