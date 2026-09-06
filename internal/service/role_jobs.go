package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

func jobPlanFromLocked(environmentID string, plan lockedPlan, inventory []byte) ansiblerunner.JobPlan {
	job := ansiblerunner.JobPlan{Contract: ansiblerunner.JobContract, EnvironmentID: environmentID, Runtime: plan.Runtime, Inventory: string(inventory), Metadata: structToMap(plan)}
	credentials := map[string]bool{}
	for _, s := range plan.Steps {
		job.Steps = append(job.Steps, ansiblerunner.JobStep{GatherFacts: s.GatherFacts, RuntimeChecks: s.RuntimeChecks, PreCheckRequired: s.PreCheckRequired, ResourceContract: s.ResourceContract, Resources: s.Resources, Media: s.Media, Stage: s.Stage, SourceType: s.SourceType, ScenarioRevisionID: s.ScenarioRevisionID, ID: s.ID, NodeID: s.SourceNodeID, Name: s.Name, ReleaseID: s.ReleaseID, ComponentID: s.ComponentID, ParentActionID: s.ParentActionID, ActionID: s.ActionID, Action: string(s.Action), Phase: s.Phase, Playbook: s.Playbook, PlaybookDigest: s.PlaybookDigest, WorkspaceDigest: s.WorkspaceDigest, Limit: s.Limit, Variables: s.Variables, TimeoutSeconds: s.TimeoutSeconds, Become: s.Become, RetrySafe: s.RetrySafe})
		for _, name := range s.RequiredCredentials {
			if !credentials[name] {
				job.RequiredCredentials = append(job.RequiredCredentials, name)
				credentials[name] = true
			}
		}
	}
	return job
}
func connectionCredentials(values map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range values {
		if strings.HasPrefix(k, "ansible_") {
			out[k] = v
		}
	}
	return out
}
func lockedStepByID(steps []lockedStep, id string) *lockedStep {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}
func parentExecutionStep(steps []lockedStep, check lockedStep) *lockedStep {
	for i := range steps {
		if steps[i].SourceNodeID == check.SourceNodeID && steps[i].ActionID == check.ParentActionID && steps[i].Phase == "execute" {
			return &steps[i]
		}
	}
	return nil
}

func (p *RunExecutor) executeLockedJob(ctx context.Context, run domain.Run, request ansiblerunner.JobRequest) (ansiblerunner.JobResult, error) {
	builder := p.jobs
	if err := p.rollback.addRecoverySteps(ctx, &request.Plan, lockedPlanFromMetadata(request.Plan.Metadata)); err != nil {
		return ansiblerunner.JobResult{}, err
	}
	bundle, err := builder.BuildJob(ctx, request.Plan)
	if err != nil {
		return ansiblerunner.JobResult{}, err
	}
	defer bundle.Close()
	if err := attachJobCompanions(bundle); err != nil {
		return ansiblerunner.JobResult{}, err
	}
	var archive bytes.Buffer
	if err := bundle.WriteArchive(&archive); err != nil {
		return ansiblerunner.JobResult{}, err
	}
	if err := p.store.SaveRunJob(ctx, run.ID, bundle.Manifest.Digest, archive.Bytes()); err != nil {
		return ansiblerunner.JobResult{}, err
	}
	result, err := builder.RunBundle(ctx, bundle, request)
	if persistErr := p.store.SetRunJobExitCode(context.Background(), run.ID, result.ExitCode); err == nil && persistErr != nil {
		err = persistErr
	}
	return result, err
}
func attachJobCompanions(bundle *ansiblerunner.JobBundle) error {
	if ansiblerunner.IsNativeJobContract(bundle.Manifest.Contract) {
		return bundle.AddFile("README.md", []byte(nativeJobReadme), false)
	}

	executable := os.Getenv("CLUSTERFORGE_JOB_CLI")
	if executable == "" {
		if self, err := os.Executable(); err == nil {
			executable = filepath.Join(filepath.Dir(self), "clusterforge-job")
		}
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return fmt.Errorf("standalone executable is unavailable; build clusterforge-job or configure CLUSTERFORGE_JOB_CLI: %w", err)
	}
	if err := bundle.AddFile("clusterforge-job", data, true); err != nil {
		return err
	}
	return bundle.AddFile("README.md", []byte("# ClusterForge role job\n\nRun `./clusterforge-job run . --credentials /private/credentials.json --results /private/results`.\nThe controller requires the exact Ansible and Python versions recorded in manifest.json.\nThe package contains no credentials. All phases run in one formal ansible-playbook invocation.\nUse `resume` with the original results to continue safely; use `rollback-preview` before `rollback`.\nInstallation media and images are verified against the locked plan and must remain accessible.\nIndependent execution records results locally and never writes platform approvals or evidence.\n"), false)
}

func lockedPlanFromMetadata(metadata map[string]any) lockedPlan {
	plan, _ := mapToPlan(metadata)
	return plan
}
func (p *RollbackPlanner) addRecoverySteps(ctx context.Context, job *ansiblerunner.JobPlan, plan lockedPlan) error {
	for i := len(plan.ParentSteps) - 1; i >= 0; i-- {
		main := plan.ParentSteps[i]
		if main.Action != domain.ActionInstall && main.Action != domain.ActionUpgrade && main.Action != domain.ActionConfigure {
			continue
		}
		release, err := p.store.GetComponentRelease(ctx, main.ReleaseID)
		if err != nil {
			return err
		}
		rollback, ok := findAction(release, domain.ActionRollback)
		if !ok {
			return domain.ErrInvalid
		}
		component, err := p.store.GetComponent(ctx, main.ComponentID, false)
		if err != nil {
			return err
		}
		rollback.HostGroup = main.Limit
		body, err := p.actions.lockAction(component, main.SourceNodeID, release, rollback, cloneMap(main.Variables))
		if err != nil {
			return err
		}
		body.SourceNodeID = main.SourceNodeID
		body.RollbackSourceActionID = main.ActionID
		body.RollbackSourceVariables = cloneMap(main.Variables)
		body.RollbackSourceFrozen = true
		body.NodeID = "recovery-" + main.ID
		body.ID = body.NodeID + "-" + rollback.ID
		if body.RetrySafe {
			body.RetrySafe, err = p.store.HasSuccessfulActionTest(ctx, body.ReleaseID, body.ReleaseSpecDigest, body.ActionID, plan.Runtime.AnsibleCore, plan.Runtime.Python)
			if err != nil {
				return err
			}
		}
		body.BackupRef, body.Backup = main.BackupRef, main.Backup
		bindBackupVariables(&body, "restore", false)
		stages, err := p.actions.expandActionSteps(ctx, []lockedStep{body})
		if err != nil {
			return err
		}
		syncActionCheckContext(stages)
		for j := range stages {
			stages[j].BackupRef = body.BackupRef
			stages[j].Backup = body.Backup
		}
		if err := p.workspaceVerifier.bindVerifiedWorkspaceDigests(ctx, stages); err != nil {
			return err
		}
		recovery := jobPlanFromLocked(job.EnvironmentID, lockedPlan{Steps: stages}, nil)
		for j := range recovery.Steps {
			recovery.Steps[j].RecoveryOfStepID = main.ID
		}
		job.Recovery = append(job.Recovery, recovery.Steps...)
		for _, name := range recovery.RequiredCredentials {
			found := false
			for _, old := range job.RequiredCredentials {
				if old == name {
					found = true
				}
			}
			if !found {
				job.RequiredCredentials = append(job.RequiredCredentials, name)
			}
		}
	}
	return nil
}

const nativeJobReadme = `# ClusterForge native Ansible job

This immutable package runs without the platform or a Go launcher.
Use the exact Ansible and controller Python versions in manifest.json.
Change into this directory and enable its ansible.cfg:

    export ANSIBLE_CONFIG="$PWD/ansible.cfg"
    export PYTHONDONTWRITEBYTECODE=1
    ansible-playbook -i inventory.ini site.yml -e @/private/run-inputs.json

Example external input (keep it private; never store credentials in this package):

    {"cf_job":{"operation":"install","results":"/private/cluster-results"},"cf_credentials":{}}

Operations: install, resume, rollback-preview, rollback. Keep the same results
path for resume and rollback. Rollback requires cf_job.expectedPlanDigest from
a current rollback-preview. Optional cf_job.nodes selects a dependency-safe
prefix of componentId/nodeId instances in reverse order. Result directories
must be private (0700); newly created directories use these permissions.

Inventory, Role source, inputs, execution order and runtime are locked. Extra
variables may only set cf_job, cf_credentials and connection authentication or
ansible_python_interpreter. Do not use --limit, --tags, --skip-tags, --start-at-task,
--step or --check to bypass stage selection. Fix source through a new version.

All original stages remain in site.yml. Resume refreshes completed upstream
checks and executes only the selected incomplete stages. Main actions require
recorded retry eligibility; successful main actions are not repeated to repair
a failed postcheck. Keep receipt.json, events.jsonl and original remote backups.

Artifacts and images must remain accessible at the locked locations. Image
transfer requires Docker on the controller. Media identity is checked before
component mutation. Credentials and registry authentication are supplied by the
operator; the package does not contain or retrieve platform secrets.

The package is bound to the recorded inventory. Independent execution does not
write platform approvals, test evidence or installation state. Inspect the
verification field in manifest.json: its absence means this is a diagnostic or
unverified package, not a verified complete cluster delivery.
`
