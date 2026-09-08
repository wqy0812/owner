package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

const RunSnapshotContract = "clusterforge-run-v1"

// RunSnapshot is the persisted execution contract. Run identity lives in Run;
// observations and delivery results are deliberately stored outside this value.
type RunSnapshot struct {
	Contract          string               `json:"contract"`
	Subject           RunSubject           `json:"subject"`
	Plan              RunPlan              `json:"plan"`
	Inputs            RunInputs            `json:"inputs"`
	Delivery          RunDeliveryPlan      `json:"delivery"`
	ScenarioExecution ScenarioRunExecution `json:"scenarioExecution,omitzero"`
	Submission        RunSubmission        `json:"submission,omitzero"`
	Retry             RunRetryContext      `json:"retry,omitzero"`
	Recovery          RunRecoveryPlan      `json:"recovery,omitzero"`
}

type RunSubject struct {
	ComponentReleaseSpecDigest string `json:"componentReleaseSpecDigest,omitempty"`
	ComponentTestEvidence      string `json:"componentTestEvidence,omitempty"`
	ScenarioID                 string `json:"scenarioId,omitempty"`
	ScenarioRevisionSpecDigest string `json:"scenarioRevisionSpecDigest,omitempty"`
}
type RunPlan struct {
	ParentSteps []RunPlanStep `json:"parentSteps"`
	Runtime     RunRuntime    `json:"runtime"`
	Steps       []RunPlanStep `json:"steps"`
	TreeDigest  string        `json:"treeDigest"`
}
type RunRuntime struct {
	AnsibleCore string `json:"ansibleCore"`
	Python      string `json:"python"`
}
type RunMedia struct {
	Kind      string `json:"kind"`
	Location  string `json:"location"`
	Identity  string `json:"identity"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}
type RunInventoryHost struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Groups  []string `json:"groups"`
	Port    int      `json:"port,omitempty"`
	User    string   `json:"user,omitempty"`
}
type RunParameterSource struct {
	Value             any    `json:"value"`
	Source            string `json:"source"`
	SourceNodeID      string `json:"sourceNodeId"`
	UpstreamParameter string `json:"upstreamParameter"`
	TargetParameter   string `json:"targetParameter"`
}
type RunInputs struct {
	ResolvedParametersByNode map[string]map[string]RunParameterSource `json:"resolvedParametersByNode,omitempty"`
	CredentialRefs           []CredentialRef                          `json:"credentialRefs"`
}
type RunDeliveryPlan struct {
	Requirements      []RunDeliveryRequirement `json:"requirements,omitempty"`
	Decisions         []RunDeliveryDecision    `json:"decisions,omitempty"`
	ArtifactTransfers []RunArtifactTransfer    `json:"artifactTransfers,omitempty"`
	ImageTransfers    []RunImageTransfer       `json:"imageTransfers,omitempty"`
}
type ScenarioTargetNode struct {
	NodeID      string         `json:"nodeId"`
	ComponentID string         `json:"componentId"`
	ReleaseID   string         `json:"releaseId"`
	Variables   map[string]any `json:"variables"`
}
type ScenarioRunBaseline struct {
	RunID              string `json:"runId"`
	Generation         int64  `json:"generation"`
	InstallationDigest string `json:"installationDigest"`
	TestOnly           bool   `json:"testOnly"`
}
type ScenarioRunExecution struct {
	Mode              ScenarioExecutionMode    `json:"mode"`
	SourceRevisionID  string                   `json:"sourceRevisionId"`
	TargetNodes       []ScenarioTargetNode     `json:"targetNodes"`
	Baseline          ScenarioRunBaseline      `json:"baseline"`
	AcceptanceJobIDs  []string                 `json:"acceptanceJobIds"`
	RecoveredReceipts []ActionExecutionReceipt `json:"recoveredReceipts,omitempty"`
}
type RunSubmission struct {
	Key        string `json:"key"`
	Digest     string `json:"digest"`
	PlanDigest string `json:"planDigest"`
}
type RunRetryContext struct {
	RecoveryStateDigest   string `json:"recoveryStateDigest"`
	BaselineGeneration    int64  `json:"baselineGeneration"`
	BaselineMutatingRunID string `json:"baselineMutatingRunId"`
}
type RunRecoveryPlan struct {
	InstallationBaseline       []RunInstallationBaseline `json:"installationBaseline,omitempty"`
	InstallationBaselineDigest string                    `json:"installationBaselineDigest,omitempty"`
	ResetTargets               []RunInventoryHost        `json:"resetTargets,omitempty"`
	ResetBoundaryDigest        string                    `json:"resetBoundaryDigest,omitempty"`
	RecoveryEnvironmentDigest  string                    `json:"recoveryEnvironmentDigest,omitempty"`
}

// SnapshotFromExecutionPlan groups a prepared plan without reinterpreting it.
// RunExecutionPlan also describes the existing Native Job metadata projection.
func SnapshotFromExecutionPlan(p RunExecutionPlan) RunSnapshot {
	return RunSnapshot{Contract: RunSnapshotContract,
		Plan:     RunPlan{ParentSteps: p.ParentSteps, Runtime: p.Runtime, Steps: p.Steps, TreeDigest: p.TreeDigest},
		Delivery: RunDeliveryPlan{Requirements: p.DeliveryRequirements, Decisions: p.DeliveryDecisions, ArtifactTransfers: p.ArtifactTransfers, ImageTransfers: p.ImageTransfers},
		Recovery: RunRecoveryPlan{InstallationBaseline: p.InstallationBaseline, InstallationBaselineDigest: p.InstallationBaselineDigest, ResetTargets: p.ResetTargets, ResetBoundaryDigest: p.ResetBoundaryDigest, RecoveryEnvironmentDigest: p.RecoveryEnvironmentDigest}}
}
func (s RunSnapshot) ExecutionPlan(results []RunDeliveryResult) RunExecutionPlan {
	return RunExecutionPlan{ParentSteps: s.Plan.ParentSteps, Runtime: s.Plan.Runtime, Steps: s.Plan.Steps, TreeDigest: s.Plan.TreeDigest,
		DeliveryRequirements: s.Delivery.Requirements, DeliveryDecisions: s.Delivery.Decisions, DeliveryResults: results, ArtifactTransfers: s.Delivery.ArtifactTransfers, ImageTransfers: s.Delivery.ImageTransfers,
		InstallationBaseline: s.Recovery.InstallationBaseline, InstallationBaselineDigest: s.Recovery.InstallationBaselineDigest, ResetTargets: s.Recovery.ResetTargets, ResetBoundaryDigest: s.Recovery.ResetBoundaryDigest, RecoveryEnvironmentDigest: s.Recovery.RecoveryEnvironmentDigest}
}

// DecodeRunSnapshot accepts exactly the current storage contract. Missing scalar
// members are checked separately because zero and false are valid locked values.
func DecodeRunSnapshot(raw []byte) (s RunSnapshot, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}()
	if err := decodeRunJSON(raw, &s); err != nil {
		return s, fmt.Errorf("run snapshot: %w", err)
	}
	if err := validateJSONShape(raw, reflect.TypeOf(s), "snapshot"); err != nil {
		return s, err
	}
	if s.Contract != RunSnapshotContract {
		return s, fmt.Errorf("unsupported run snapshot contract %q", s.Contract)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return s, err
	}
	for _, key := range []string{"subject", "plan", "inputs", "delivery"} {
		if len(members[key]) == 0 || bytes.Equal(members[key], []byte("null")) {
			return s, fmt.Errorf("run snapshot.%s is required", key)
		}
	}
	if v, ok := members["scenarioExecution"]; ok {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(v, &fields)
		for _, key := range []string{"mode", "sourceRevisionId", "targetNodes", "baseline", "acceptanceJobIds"} {
			if len(fields[key]) == 0 || bytes.Equal(fields[key], []byte("null")) {
				return s, fmt.Errorf("run snapshot.scenarioExecution.%s is required", key)
			}
		}
		var baseline map[string]json.RawMessage
		_ = json.Unmarshal(fields["baseline"], &baseline)
		for _, key := range []string{"runId", "generation", "installationDigest", "testOnly"} {
			if len(baseline[key]) == 0 || bytes.Equal(baseline[key], []byte("null")) {
				return s, fmt.Errorf("run snapshot.scenarioExecution.baseline.%s is required", key)
			}
		}
	}
	return s, nil
}

// DecodeRunDeliveryResults reads observations independently of the frozen plan.
func DecodeRunDeliveryResults(raw []byte) ([]RunDeliveryResult, error) {
	var results []RunDeliveryResult
	if err := decodeRunJSON(raw, &results); err != nil {
		return nil, fmt.Errorf("%w: delivery results: %w", ErrInvalid, err)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%w: delivery results must be an array", ErrInvalid)
	}
	if err := validateJSONShape(raw, reflect.TypeOf(results), "deliveryResults"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return results, nil
}

func decodeRunJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
func EncodeRunSnapshot(s RunSnapshot) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if _, err = DecodeRunSnapshot(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
func (s RunSnapshot) Clone() (RunSnapshot, error) {
	raw, err := EncodeRunSnapshot(s)
	if err != nil {
		return RunSnapshot{}, err
	}
	return DecodeRunSnapshot(raw)
}

func ValidateRunSnapshot(r Run) error {
	s := r.Snapshot
	bad := func(path, reason string) error {
		return fmt.Errorf("%w: run snapshot.%s: %s", ErrInvalid, path, reason)
	}
	if s.Contract != RunSnapshotContract {
		return bad("contract", "unsupported contract")
	}
	if len(s.Plan.Steps) == 0 {
		return bad("plan.steps", "locked run plan contains no steps")
	}
	seen := map[string]bool{}
	for _, step := range s.Plan.Steps {
		if step.ID == "" || step.NodeID == "" {
			return bad("plan.steps", "step identity is required")
		}
		if seen[step.ID] {
			return bad("plan.steps", "duplicate step identity")
		}
		seen[step.ID] = true
		if step.SourceType != "" && step.SourceType != "component_action" && step.SourceType != "scenario_acceptance" {
			return bad("plan.steps.sourceType", "unsupported source")
		}
		if step.SourceType == "scenario_acceptance" && (step.ReleaseID != "" || step.ComponentID != "" || step.ScenarioRevisionID != r.ScenarioRevisionID || step.AcceptanceJobID == "") {
			return bad("plan.steps", "invalid scenario acceptance identity")
		}
	}
	scenario := r.Kind == RunScenario || r.Kind == RunScenarioTest
	if scenario {
		if r.ScenarioRevisionID == "" || s.Subject.ScenarioID == "" || s.Subject.ScenarioRevisionSpecDigest == "" {
			return bad("subject", "scenario identity and digest are required")
		}
		x := s.ScenarioExecution
		switch x.Mode {
		case ScenarioExecutionInstall, ScenarioExecutionUpgrade, ScenarioExecutionBaselineVerify:
		default:
			return bad("scenarioExecution.mode", "unsupported mode")
		}
		if x.TargetNodes == nil || x.AcceptanceJobIDs == nil {
			return bad("scenarioExecution", "target nodes and acceptance jobs must be arrays")
		}
		if x.Baseline.Generation < 0 || x.Baseline.InstallationDigest == "" {
			return bad("scenarioExecution.baseline", "invalid baseline")
		}
		if x.Mode == ScenarioExecutionUpgrade && x.SourceRevisionID == "" {
			return bad("scenarioExecution.sourceRevisionId", "upgrade source is required")
		}
		if x.Mode != ScenarioExecutionBaselineVerify && len(x.RecoveredReceipts) > 0 {
			return bad("scenarioExecution.recoveredReceipts", "only baseline verification can recover receipts")
		}
		if x.Mode == ScenarioExecutionBaselineVerify && (len(s.Delivery.Requirements) > 0 || len(s.Delivery.ArtifactTransfers) > 0 || len(s.Delivery.ImageTransfers) > 0) {
			return bad("delivery", "基线复核不能执行新的媒体交付")
		}
		if s.Submission.Key == "" || s.Submission.Digest == "" || s.Submission.PlanDigest == "" {
			return bad("submission", "submission identity and digests are required")
		}
	} else {
		if r.Kind != RunComponentTest && r.Kind != RunEnvironmentRollback {
			return bad("kind", "unsupported Run kind")
		}
		if !reflect.ValueOf(s.ScenarioExecution).IsZero() {
			return bad("scenarioExecution", "not a scenario execution")
		}
	}
	if r.RetryOfRunID != "" && s.Retry.RecoveryStateDigest == "" {
		return bad("retry.recoveryStateDigest", "retry recovery digest is required")
	}
	if r.RetryOfRunID == "" && !reflect.ValueOf(s.Retry).IsZero() {
		return bad("retry", "unexpected retry context")
	}
	if s.Retry.BaselineGeneration < 0 {
		return bad("retry.baselineGeneration", "negative generation")
	}
	nodes := map[string]bool{}
	for _, n := range s.ScenarioExecution.TargetNodes {
		if n.NodeID == "" || n.ComponentID == "" || n.ReleaseID == "" || nodes[n.NodeID] {
			return bad("scenarioExecution.targetNodes", "invalid or duplicate target identity")
		}
		nodes[n.NodeID] = true
	}
	jobs := map[string]bool{}
	for _, id := range s.ScenarioExecution.AcceptanceJobIDs {
		if id == "" || jobs[id] {
			return bad("scenarioExecution.acceptanceJobIds", "invalid or duplicate job identity")
		}
		jobs[id] = true
	}
	requirements := map[string]RunDeliveryRequirement{}
	for _, v := range s.Delivery.Requirements {
		if v.ID == "" || requirements[v.ID].ID != "" || (v.Kind != "artifact" && v.Kind != "image") {
			return bad("delivery.requirements", "invalid requirement identity or kind")
		}
		requirements[v.ID] = v
	}
	decisions := map[string]string{}
	for _, v := range s.Delivery.Decisions {
		if requirements[v.RequirementID].ID == "" || decisions[v.RequirementID] != "" || (v.Mode != "direct" && v.Mode != "transfer") {
			return bad("delivery.decisions", "invalid or contradictory selection")
		}
		decisions[v.RequirementID] = v.Mode
	}
	transfers := map[string]bool{}
	for _, v := range s.Delivery.ArtifactTransfers {
		if requirements[v.RequirementID].Kind != "artifact" || decisions[v.RequirementID] != "transfer" || transfers[v.RequirementID] {
			return bad("delivery.artifactTransfers", "transfer differs from selection")
		}
		transfers[v.RequirementID] = true
	}
	for _, v := range s.Delivery.ImageTransfers {
		if requirements[v.RequirementID].Kind != "image" || decisions[v.RequirementID] != "transfer" || transfers[v.RequirementID] {
			return bad("delivery.imageTransfers", "transfer differs from selection")
		}
		transfers[v.RequirementID] = true
	}
	if (r.Status == RunQueued || r.Status == RunRunning) && len(requirements) != len(decisions) {
		return bad("delivery.decisions", "queued delivery requires complete approval")
	}
	return nil
}

// ReferencedRunIDs walks authoritative identities only, never user variables or
// parameter provenance. It is shared by creation, approval and offline migration.
func (s RunSnapshot) ReferencedRunIDs() []string {
	ids := map[string]bool{}
	add := func(id string) {
		if id != "" {
			ids[id] = true
		}
	}
	var backup func(*BackupMetadata)
	backup = func(b *BackupMetadata) {
		if b == nil {
			return
		}
		add(b.InstallRunID)
		if b.Previous != nil {
			add(b.Previous.InstallRunID)
			backup(&b.Previous.Backup)
		}
	}
	for _, steps := range [][]RunPlanStep{s.Plan.Steps, s.Plan.ParentSteps} {
		for _, step := range steps {
			backup(step.Backup)
		}
	}
	for _, b := range s.Recovery.InstallationBaseline {
		add(b.InstallRunID)
	}
	add(s.ScenarioExecution.Baseline.RunID)
	add(s.Retry.BaselineMutatingRunID)
	for _, r := range s.ScenarioExecution.RecoveredReceipts {
		add(r.RunID)
		backup(&r.Backup)
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s RunSnapshot) ReleaseLocks() (map[string]string, error) {
	locks := map[string]string{}
	for _, steps := range [][]RunPlanStep{s.Plan.Steps, s.Plan.ParentSteps} {
		for _, step := range steps {
			if step.SourceType == "scenario_acceptance" {
				continue
			}
			if step.ReleaseID == "" || step.ReleaseSpecDigest == "" {
				return nil, fmt.Errorf("%w: locked step is missing release definition digest", ErrConflict)
			}
			if previous, ok := locks[step.ReleaseID]; ok && previous != step.ReleaseSpecDigest {
				return nil, fmt.Errorf("%w: conflicting locked release definitions", ErrConflict)
			}
			locks[step.ReleaseID] = step.ReleaseSpecDigest
		}
	}
	if len(s.Plan.Steps) == 0 {
		return nil, fmt.Errorf("%w: no locked steps", ErrConflict)
	}
	return locks, nil
}

// validateJSONShape preserves presence independently of decoded Go zero values.
// Parameters are intentionally open values; platform objects use exact JSON names.
func validateJSONShape(raw json.RawMessage, typ reflect.Type, path string) error {
	if typ.Kind() == reflect.Interface {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		return validateJSONShape(raw, typ.Elem(), path)
	}
	if bytes.Equal(raw, []byte("null")) {
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			return nil
		}
		return fmt.Errorf("run %s must not be null", path)
	}
	// time.Time and json.RawMessage own their wire representation.
	if typ.Implements(reflect.TypeFor[json.Marshaler]()) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("run %s: %w", path, err)
		}
		known := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tags := strings.Split(f.Tag.Get("json"), ",")
			name := tags[0]
			if name == "-" || !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			known[name] = true
			value, ok := fields[name]
			optional := len(tags) > 1 && (tags[1] == "omitempty" || tags[1] == "omitzero")
			if !ok {
				if !optional {
					return fmt.Errorf("run %s.%s is required", path, name)
				}
				continue
			}
			if err := validateJSONShape(value, f.Type, path+"."+name); err != nil {
				return err
			}
		}
		for name := range fields {
			if !known[name] {
				return fmt.Errorf("run %s.%s is unknown", path, name)
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for i, v := range values {
			if err := validateJSONShape(v, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if typ.Elem().Kind() == reflect.Interface {
			return nil
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for key, v := range values {
			if err := validateJSONShape(v, typ.Elem(), path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}
