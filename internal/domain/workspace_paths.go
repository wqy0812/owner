package domain

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Fixed-depth roots make component workspaces siblings rather than owners of
// one another's trees. Auxiliary subdirectories remain local to one root.
func ValidateComponentWorkspaceRoot(root string) error {
	clean := strings.TrimSuffix(root, "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || parts[0] != "managed" || path.Clean(clean) != clean || strings.ContainsAny(clean, "\\\x00") {
		return fmt.Errorf("%w: component workspace must be a platform-assigned managed/component/line/release directory", ErrInvalid)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid workspace segment", ErrInvalid)
		}
	}
	return nil
}

func WorkspaceSegment(value string) (string, error) {
	value = strings.TrimSpace(value)
	var out strings.Builder
	separator := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			out.WriteRune(unicode.ToLower(r))
			separator = false
		case r == '.' || r == '_' || r == '-':
			if out.Len() > 0 && !separator {
				out.WriteRune(r)
				separator = true
			}
		case unicode.IsSpace(r):
			if out.Len() > 0 && !separator {
				out.WriteByte('-')
				separator = true
			}
		default:
			if out.Len() > 0 && !separator {
				out.WriteByte('-')
				separator = true
			}
		}
	}
	result := strings.Trim(out.String(), "._-")
	if result == "" || result == "." || result == ".." {
		return "", fmt.Errorf("%w: value cannot form a safe workspace directory", ErrInvalid)
	}
	return result, nil
}

func GeneratedComponentWorkspaceRoot(component Component, release ComponentRelease) string {
	componentKey, err := WorkspaceSegment(component.Slug)
	if err != nil {
		componentKey = component.ID
	}
	lineKey, err := WorkspaceSegment(release.LineName)
	if err != nil {
		lineKey = release.LineID
	}
	versionKey, err := WorkspaceSegment(release.Version)
	if err != nil {
		versionKey = release.ID
	}
	// Human-readable names are not identities: different names can normalize to
	// the same segment. The immutable full Release ID makes every physical root
	// unambiguous without sacrificing operator readability.
	releaseKey, err := WorkspaceSegment(release.ID)
	if err != nil {
		return "managed/" + componentKey + "/" + lineKey + "/" + release.ID + "/"
	}
	return "managed/" + componentKey + "/" + lineKey + "/" + versionKey + "--" + releaseKey + "/"
}
