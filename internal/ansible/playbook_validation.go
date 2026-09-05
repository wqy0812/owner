package ansible

import "fmt"

// ValidatePlaybooks checks the same paths, file readability and executable tree
// as Digest, but scans the shared tree once for a read-only catalog response.
// It returns an entry for every requested path, with nil for a valid path.
// Results belong to this call only; they are not execution locks. Queueing and
// execution must continue to use DigestPlan and the verified workspace.
func (r *Runner) ValidatePlaybooks(playbooks []string) map[string]error {
	return r.validatePlaybooks(playbooks, TreeDigest)
}

func (r *Runner) validatePlaybooks(playbooks []string, treeDigest func(string) (string, error)) map[string]error {
	results := make(map[string]error, len(playbooks))
	roots := map[string][]string{}
	for _, relative := range playbooks {
		if _, checked := results[relative]; checked {
			continue
		}
		resolvedRoot, playbook, clean, err := r.resolvePlaybook(relative)
		if err == nil {
			if _, digestErr := FileDigest(playbook); digestErr != nil {
				err = fmt.Errorf("digest playbook: %w", digestErr)
			}
		}
		results[relative] = err
		if err == nil {
			root := workspaceRoot(resolvedRoot, playbook, clean)
			roots[root] = append(roots[root], relative)
		}
	}
	for root, relatives := range roots {
		if _, err := treeDigest(root); err != nil {
			for _, relative := range relatives {
				results[relative] = fmt.Errorf("digest executable workspace: %w", err)
			}
		}
	}
	return results
}
