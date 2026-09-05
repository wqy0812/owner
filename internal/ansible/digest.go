package ansible

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func workspaceRoot(root, playbook, clean string) string {
	parts := strings.Split(filepath.ToSlash(clean), "/")
	if len(parts) >= 4 && parts[0] == "managed-scenarios" {
		return filepath.Join(root, filepath.FromSlash(strings.Join(parts[:3], "/")))
	}
	if len(parts) >= 5 && parts[0] == "managed" {
		return filepath.Join(root, filepath.FromSlash(strings.Join(parts[:4], "/")))
	}
	return root
}

// Digest validates an allow-listed relative playbook and returns both its file
// digest and the complete executable-tree digest. A Run stores these before it
// enters the queue and verifies the same tree again during execution.
func (r *Runner) Digest(relative string) (playbookSHA256, treeSHA256 string, err error) {
	root, playbook, clean, err := r.resolvePlaybook(relative)
	if err != nil {
		return "", "", err
	}
	playbookSHA256, err = FileDigest(playbook)
	if err != nil {
		return "", "", fmt.Errorf("digest playbook: %w", err)
	}
	treeSHA256, err = TreeDigest(workspaceRoot(root, playbook, clean))
	if err != nil {
		return "", "", fmt.Errorf("digest allowed tree: %w", err)
	}
	return playbookSHA256, treeSHA256, nil
}

// DigestPlan locks every referenced playbook and the shared executable tree.
// The tree is intentionally walked once regardless of the number of steps.
func (r *Runner) DigestPlan(playbooks []string) (map[string]string, string, error) {
	digests := make(map[string]string, len(playbooks))
	workspaceDigests := map[string]string{}
	for _, relative := range playbooks {
		resolvedRoot, playbook, clean, err := r.resolvePlaybook(relative)
		if err != nil {
			return nil, "", err
		}
		workspace := workspaceRoot(resolvedRoot, playbook, clean)
		if _, exists := digests[relative]; exists {
			continue
		}
		digest, err := FileDigest(playbook)
		if err != nil {
			return nil, "", fmt.Errorf("digest playbook: %w", err)
		}
		digests[relative] = digest
		if _, exists := workspaceDigests[workspace]; !exists {
			workspaceDigest, err := TreeDigest(workspace)
			if err != nil {
				return nil, "", fmt.Errorf("digest executable workspace: %w", err)
			}
			workspaceDigests[workspace] = workspaceDigest
		}
	}
	if len(workspaceDigests) == 0 {
		return digests, "", nil
	}
	if len(workspaceDigests) == 1 {
		for _, digest := range workspaceDigests {
			return digests, digest, nil
		}
	}
	paths := make([]string, 0, len(workspaceDigests))
	for path := range workspaceDigests {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(filepath.ToSlash(path)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(workspaceDigests[path]))
		_, _ = hash.Write([]byte{0})
	}
	return digests, hex.EncodeToString(hash.Sum(nil)), nil
}
