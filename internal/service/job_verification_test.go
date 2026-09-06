package service

import (
	"testing"

	"codex/platform-demo/internal/ansible"
)

func TestVerifiedJobRejectsChangedExecution(t *testing.T) {
	original := ansible.JobStep{ID: "post", NodeID: "node", ComponentID: "component", ActionID: "postcheck", Phase: "post", Playbook: "tasks/post.yml", PlaybookDigest: "source", WorkspaceDigest: "tree", Limit: "all", Variables: map[string]any{"port": 6443}, RetrySafe: true}
	refresh := original
	refresh.ID, refresh.Phase = "refresh-post", "check"
	if !sameVerifiedJobStep(original, refresh) {
		t.Fatal("valid upstream verification rejected")
	}
	for name, mutate := range map[string]func(*ansible.JobStep){
		"source":       func(s *ansible.JobStep) { s.PlaybookDigest = "changed" },
		"workspace":    func(s *ansible.JobStep) { s.WorkspaceDigest = "changed" },
		"node":         func(s *ansible.JobStep) { s.NodeID = "another" },
		"hosts":        func(s *ansible.JobStep) { s.Limit = "subset" },
		"parameters":   func(s *ansible.JobStep) { s.Variables = map[string]any{"port": 1234} },
		"retry policy": func(s *ansible.JobStep) { s.RetrySafe = false },
		"action":       func(s *ansible.JobStep) { s.ActionID = "different" },
		"phase":        func(s *ansible.JobStep) { s.Phase = "execute" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := refresh
			mutate(&candidate)
			if sameVerifiedJobStep(original, candidate) {
				t.Fatal("changed execution accepted as original evidence")
			}
		})
	}
}
