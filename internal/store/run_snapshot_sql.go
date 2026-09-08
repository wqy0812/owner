package store

// SQL literals for typed snapshot projections. The DDL uses the same paths in
// schema.sql; contract projection tests bind both to shared domain samples.
const (
	runSnapshotContractPath        = "'$.contract'"
	runSnapshotStepsPath           = "'$.plan.steps'"
	runSnapshotParentsPath         = "'$.plan.parentSteps'"
	runSnapshotAnsiblePath         = "'$.plan.runtime.ansibleCore'"
	runSnapshotPythonPath          = "'$.plan.runtime.python'"
	runSnapshotComponentDigestPath = "'$.subject.componentReleaseSpecDigest'"
	runSnapshotScenarioDigestPath  = "'$.subject.scenarioRevisionSpecDigest'"
	runSnapshotRequirementsPath    = "'$.delivery.requirements'"
	runSnapshotScenarioModePath    = "'$.scenarioExecution.mode'"
	runSnapshotAcceptanceJobsPath  = "'$.scenarioExecution.acceptanceJobIds'"
)

// Only selected identities request action topology from the real Run. Keeping
// this out of retained_run_history avoids expanding JSON work while ranking or
// evaluating permissions over the complete history.
const runReadStepObjectSQL = `json_object('id',json_extract(value,'$.id'),'nodeId',json_extract(value,'$.nodeId'),'name',json_extract(value,'$.name'),'componentId',json_extract(value,'$.componentId'),'componentName',json_extract(value,'$.componentName'),'releaseId',json_extract(value,'$.releaseId'),'releaseSpecDigest',json_extract(value,'$.releaseSpecDigest'),'phase',json_extract(value,'$.phase'),'action',json_extract(value,'$.action'),'actionId',json_extract(value,'$.actionId'),'parentActionId',json_extract(value,'$.parentActionId'),'sourceNodeId',json_extract(value,'$.sourceNodeId'),'sourceType',json_extract(value,'$.sourceType'),'scenarioRevisionId',json_extract(value,'$.scenarioRevisionId'),'stage',json_extract(value,'$.stage'))`
const runReadStepsSQL = `CASE WHEN cleaned=1 THEN (SELECT release_locks_json FROM run_cleanup_history h WHERE h.id=runs.id) ELSE
 (SELECT json_group_array(` + runReadStepObjectSQL + `) FROM runs live,json_each(live.execution_snapshot_json,` + runSnapshotStepsPath + `) WHERE live.id=runs.id) END`
const runReadParentsSQL = `CASE WHEN cleaned=1 THEN '[]' ELSE
 (SELECT json_group_array(` + runReadStepObjectSQL + `) FROM runs live,json_each(live.execution_snapshot_json,` + runSnapshotParentsPath + `) WHERE live.id=runs.id) END`
