package ansible

import "fmt"

// Digest validates an allow-listed relative playbook and returns both its file
// digest and the complete executable-tree digest. A Run stores these before it
// enters the queue and verifies the same tree again during execution.
func (r *Runner) Digest(relative string) (playbookSHA256, treeSHA256 string, err error) {
	root, playbook, _, err := r.resolvePlaybook(relative)
	if err != nil {
		return "", "", err
	}
	playbookSHA256, err = FileDigest(playbook)
	if err != nil {
		return "", "", fmt.Errorf("digest playbook: %w", err)
	}
	treeSHA256, err = TreeDigest(root)
	if err != nil {
		return "", "", fmt.Errorf("digest allowed tree: %w", err)
	}
	return playbookSHA256, treeSHA256, nil
}

// DigestPlan locks every referenced playbook and the shared executable tree.
// The tree is intentionally walked once regardless of the number of steps.
func (r *Runner) DigestPlan(playbooks []string) (map[string]string, string, error) {
	digests := make(map[string]string, len(playbooks))
	var root string
	for _, relative := range playbooks {
		resolvedRoot, playbook, _, err := r.resolvePlaybook(relative)
		if err != nil {
			return nil, "", err
		}
		if root == "" {
			root = resolvedRoot
		} else if root != resolvedRoot {
			return nil, "", fmt.Errorf("playbooks resolved to different executable trees")
		}
		if _, exists := digests[relative]; exists {
			continue
		}
		digest, err := FileDigest(playbook)
		if err != nil {
			return nil, "", fmt.Errorf("digest playbook: %w", err)
		}
		digests[relative] = digest
	}
	if root == "" {
		return digests, "", nil
	}
	treeDigest, err := TreeDigest(root)
	if err != nil {
		return nil, "", fmt.Errorf("digest allowed tree: %w", err)
	}
	return digests, treeDigest, nil
}
