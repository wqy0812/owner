package delivery

import (
	"codex/platform-demo/internal/domain"
	"fmt"
	"regexp"
	"strings"
)

var ociDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func NormalizeOCIDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !ociDigestPattern.MatchString(value) {
		return "", fmt.Errorf("%w: image digest must be sha256 followed by 64 lowercase hexadecimal characters", domain.ErrInvalid)
	}
	return value, nil
}

func DigestFromResolvedImageRef(value string) (string, error) {
	index := strings.LastIndex(value, "@")
	if index < 0 {
		return "", fmt.Errorf("%w: registry did not return an immutable image digest", domain.ErrConflict)
	}
	return NormalizeOCIDigest(value[index+1:])
}

func ImmutableImageSourceRef(sourceRef, digest string) string {
	repository := sourceRef
	if at := strings.LastIndex(repository, "@"); at >= 0 {
		repository = repository[:at]
	}
	if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
		repository = repository[:colon]
	}
	return repository + "@" + digest
}
