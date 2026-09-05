package domain

import "testing"

func TestScenarioLifecycleDigestKeepsHistoricalAlgorithm(t *testing.T) {
	legacy := ScenarioRevision{Graph: ScenarioGraph{Nodes: []ScenarioNode{{ID: "node", ReleaseID: "release"}}}}
	old := LegacyScenarioRevisionSpecDigest(legacy)
	legacy.AcceptanceJobs = []ScenarioAcceptanceJob{{ID: "acceptance", PlaybookSHA256: "a"}}
	if got := ScenarioRevisionSpecDigest(legacy); got != old {
		t.Fatalf("legacy algorithm changed: %s != %s", got, old)
	}
	legacy.DigestVersion = ScenarioDigestVersion
	current := ScenarioRevisionSpecDigest(legacy)
	legacy.AcceptanceJobs[0].PlaybookSHA256 = "b"
	if ScenarioRevisionSpecDigest(legacy) == current {
		t.Fatal("acceptance content did not invalidate evidence")
	}
	current = ScenarioRevisionSpecDigest(legacy)
	legacy.SourceRunID = "formal-source"
	if ScenarioRevisionSpecDigest(legacy) == current {
		t.Fatal("source identity not bound")
	}
	current = ScenarioRevisionSpecDigest(legacy)
	legacy.Status = RevisionReleased
	legacy.PublicationGeneration = 5
	if ScenarioRevisionSpecDigest(legacy) != current {
		t.Fatal("publication bookkeeping changed evidence identity")
	}
}
