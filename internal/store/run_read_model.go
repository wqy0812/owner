package store

import (
	"database/sql"
	"encoding/json"

	"codex/platform-demo/internal/domain"
)

const runReadColumns = `id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,retry_start_step,error_text,created_at,started_at,finished_at,component_spec_digest,scenario_spec_digest,component_evidence_kind,cleaned`
const runReadSelect = `SELECT ` + runReadColumns + `,` + runReadStepsSQL + `,` + runReadParentsSQL + ` FROM retained_run_history runs`

func scanRunReadModel(row scanner) (domain.RunReadModel, error) {
	var r domain.RunReadModel
	var component, scenario, retryOf, retryRoot, componentDigest, scenarioDigest, evidence sql.NullString
	var started, finished sql.NullString
	var destructive, cleaned int
	var created, steps, parents string
	err := row.Scan(&r.ID, &r.Kind, &r.Status, &r.RequestedBy, &r.EnvironmentID, &r.EnvironmentRevisionID, &component, &scenario, &r.Action, &destructive, &r.ArtifactDigest, &retryOf, &retryRoot, &r.RetryAttempt, &r.RetryStartStep, &r.Error, &created, &started, &finished, &componentDigest, &scenarioDigest, &evidence, &cleaned, &steps, &parents)
	if err != nil {
		return r, err
	}
	r.ComponentReleaseID, r.ScenarioRevisionID = component.String, scenario.String
	r.RetryOfRunID, r.RetryRootRunID = retryOf.String, retryRoot.String
	r.Destructive, r.Cleaned = destructive != 0, cleaned != 0
	r.CreatedAt, r.StartedAt, r.FinishedAt = parseTime(created), parseNullTime(started), parseNullTime(finished)
	r.Subject = domain.RunSubject{ComponentReleaseSpecDigest: componentDigest.String, ScenarioRevisionSpecDigest: scenarioDigest.String, ComponentTestEvidence: evidence.String}
	if err = json.Unmarshal([]byte(steps), &r.LockedSteps); err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(parents), &r.ParentSteps); err != nil {
		return r, err
	}
	return r, nil
}
