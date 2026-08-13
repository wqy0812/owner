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
