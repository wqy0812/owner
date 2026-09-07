package domain

import "testing"

func TestScenarioDigestIncludesAcceptanceAndUpgradeSource(t *testing.T) {
	revision := ScenarioRevision{Graph: ScenarioGraph{Nodes: []ScenarioNode{{ID: "node", ReleaseID: "release"}}}}
	old := ScenarioRevisionSpecDigest(revision)
	revision.AcceptanceJobs = []ScenarioAcceptanceJob{{ID: "acceptance", PlaybookSHA256: "a"}}
	if got := ScenarioRevisionSpecDigest(revision); got == old {
		t.Fatalf("acceptance definition omitted from digest: %s == %s", got, old)
	}
	current := ScenarioRevisionSpecDigest(revision)
	revision.AcceptanceJobs[0].PlaybookSHA256 = "b"
	if ScenarioRevisionSpecDigest(revision) == current {
		t.Fatal("acceptance content did not invalidate evidence")
	}
	current = ScenarioRevisionSpecDigest(revision)
	revision.SourceRunID = "formal-source"
	if ScenarioRevisionSpecDigest(revision) == current {
		t.Fatal("source identity not bound")
	}
	current = ScenarioRevisionSpecDigest(revision)
	revision.Status = RevisionReleased
	revision.PublicationGeneration = 5
	if ScenarioRevisionSpecDigest(revision) != current {
		t.Fatal("publication bookkeeping changed evidence identity")
	}
}
