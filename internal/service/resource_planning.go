package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

type resourcePlanStore interface {
	GetComponentRelease(context.Context, string) (domain.ComponentRelease, error)
	GetRun(context.Context, string) (domain.Run, error)
	ListEnvironmentComponentInstallations(context.Context, string) ([]domain.EnvironmentComponentInstallation, error)
}

func resourceHostNames(inventory InventoryDocument, group string) []string {
	hosts := []string{}
	for _, h := range inventory.Hosts {
		for _, g := range append(append([]string{}, h.Groups...), "all") {
			if g == group {
				address := strings.ToLower(strings.TrimSuffix(h.Address, "."))
				if ip, err := netip.ParseAddr(address); err == nil {
					address = ip.Unmap().String()
				}
				if address == "localhost" || address == "::1" {
					address = "127.0.0.1"
				}
				hosts = append(hosts, address)
				break
			}
		}
	}
	return hosts
}
func resourceOwner(step lockedStep) string {
	if step.SourceNodeID != "" {
		return step.SourceNodeID
	}
	return step.NodeID
}
func resolveStepResources(step lockedStep, inventory InventoryDocument) ([]domain.ResourceInstance, error) {
	claims, err := domain.ResolveResourceContract(step.ResourceContract, step.Variables)
	if err != nil {
		return nil, err
	}
	instances := []domain.ResourceInstance{}
	hosts := resourceHostNames(inventory, step.Limit)
	if len(claims) > 0 && len(hosts) == 0 {
		return nil, fmt.Errorf("%w: resource host group %s has no concrete hosts", domain.ErrConflict, step.Limit)
	}
	for _, claim := range claims {
		instances = append(instances, domain.ResourceInstance{OwnerID: resourceOwner(step), ReleaseID: step.ReleaseID, ActionID: step.ActionID, HostGroup: step.Limit, Hosts: hosts, Claim: claim})
	}
	return instances, nil
}
func validateResourcePlan(ctx context.Context, data resourcePlanStore, environment domain.Environment, plan *lockedPlan, continuingRuns ...string) error {
	var inventory InventoryDocument
	if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil {
		return err
	}
	releases := map[string]domain.ComponentRelease{}
	instances := []domain.ResourceInstance{}
	replacing := map[string]bool{}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		// Recovery and source verification consume their original locked contract.
		if step.SourceType == "scenario_acceptance" || (step.SourceParametersFrozen && step.RollbackSourceActionID == "") || ((step.Action == domain.ActionUninstall || step.Action == domain.ActionRollback) && step.BackupRef != "") {
			continue
		}
		release, ok := releases[step.ReleaseID]
		if !ok {
			var err error
			release, err = data.GetComponentRelease(ctx, step.ReleaseID)
			if err != nil {
				return err
			}
			releases[release.ID] = release
		}
		if err := domain.ValidateResourceContract(step.ResourceContract, release.Parameters, true); err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
		for _, claim := range step.ResourceContract.Claims {
			if claim.SharedWith != nil {
				found := false
				for _, dep := range release.Dependencies {
					found = found || dep.UpstreamReleaseID == claim.SharedWith.ReleaseID
				}
				if !found {
					return fmt.Errorf("%w: shared resource must reference a declared exact dependency", domain.ErrInvalid)
				}
			}
		}
		resolved, err := resolveStepResources(*step, inventory)
		if err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
		if len(step.ResourceContract.Checks) > 0 || step.GatherFacts || len(step.RuntimeChecks) > 0 {
			return domain.YAMLMigrationRequired(step.Name)
		}
		step.Resources = resolved
		instances = append(instances, resolved...)
		if step.FromReleaseID != "" && (step.Action == domain.ActionUpgrade || step.Action == domain.ActionConfigure) {
			replacing[step.ComponentID+"\x00"+step.FromReleaseID+"\x00"+resourceOwner(*step)] = true
		}
	}
	plan.ExistingResources = nil
	installed, err := data.ListEnvironmentComponentInstallations(ctx, environment.ID)
	if err != nil {
		return err
	}
	for _, item := range installed {
		continuing := false
		for _, id := range continuingRuns {
			if id != "" && item.InstallRunID == id {
				continuing = true
			}
		}
		if continuing {
			continue
		}
		if replacing[item.ComponentID+"\x00"+item.ReleaseID+"\x00"+item.NodeID] {
			continue
		}
		run, err := data.GetRun(ctx, item.InstallRunID)
		if err != nil {
			return err
		}
		locked, err := mapToPlan(run.InputSnapshot)
		if err != nil {
			return &domain.CodedError{Code: "resource.installed_snapshot_missing", Message: "环境安装基线缺少可校验的锁定计划，请先核实已有安装", Cause: domain.ErrConflict}
		}
		matched := false
		for _, original := range locked.Steps {
			if original.ReleaseID != item.ReleaseID || original.ComponentID != item.ComponentID || (item.NodeID != "" && resourceOwner(original) != item.NodeID) || original.Phase != "execute" {
				continue
			}
			if original.ResourceContract == nil {
				return &domain.CodedError{Code: "resource.installed_contract_missing", Message: "环境已有安装记录缺少资源声明，需先确认并更新安装基线", Cause: domain.ErrConflict}
			}
			matched = true
			original.SourceNodeID = item.NodeID
			if original.SourceNodeID == "" {
				original.SourceNodeID = "installed:" + item.ComponentID
			}
			// Installation ownership belongs to the original concrete hosts. A
			// later inventory revision must not move an existing installation.
			resolved := append([]domain.ResourceInstance{}, original.Resources...)
			if len(resolved) != len(original.ResourceContract.Claims) {
				return &domain.CodedError{Code: "resource.installed_hosts_missing", Message: "已有安装缺少原始主机资源快照，请核实安装基线", Cause: domain.ErrConflict}
			}
			for i := range resolved {
				if len(resolved[i].Hosts) == 0 {
					return fmt.Errorf("%w: installed resource has no original hosts", domain.ErrConflict)
				}
				resolved[i].OwnerID = original.SourceNodeID
			}
			instances = append(instances, resolved...)
			plan.ExistingResources = append(plan.ExistingResources, resolved...)
		}
		if !matched {
			return &domain.CodedError{Code: "resource.installed_snapshot_missing", Message: "安装清单与原作业执行步骤无法对应，请核实安装基线", Cause: domain.ErrConflict}
		}
	}
	if issues := domain.ResourceConflicts(instances); len(issues) > 0 {
		return &domain.CodedError{Code: issues[0].Code, Message: issues[0].Message, Cause: domain.ErrConflict, Details: map[string]any{"issues": issues}}
	}
	plan.ResourcePolicyVersion = domain.ResourceContractVersion
	return nil
}

// Static validation uses fixed values only; environment-bound paths and
// host-group intersections are resolved again for the concrete execution.
func scenarioResourceIssues(graph domain.ScenarioGraph, releases map[string]domain.ComponentRelease) []domain.ValidationIssue {
	instances := []domain.ResourceInstance{}
	issues := []domain.ValidationIssue{}
	for _, node := range graph.Nodes {
		release, ok := releases[node.ReleaseID]
		if !ok {
			continue
		}
		values := map[string]any{}
		for _, p := range release.Parameters {
			if p.FixedValue != nil {
				values[p.Name] = p.FixedValue
			}
		}
		for k, v := range node.ParameterValues {
			values[k] = v
		}
		for _, action := range release.Actions {
			if err := domain.ValidateResourceContract(action.ResourceContract, release.Parameters, true); err != nil {
				issues = append(issues, domain.ValidationIssue{Code: "resource.contract_missing", Message: err.Error(), NodeID: node.ID})
				continue
			}
			resolved, err := domain.ResolveResourceContract(action.ResourceContract, values)
			if err != nil {
				if !strings.Contains(err.Error(), "is unresolved") {
					issues = append(issues, domain.ValidationIssue{Code: "resource.contract_invalid", Message: err.Error(), NodeID: node.ID})
				}
				continue
			}
			for _, claim := range resolved {
				instances = append(instances, domain.ResourceInstance{OwnerID: node.ID, ReleaseID: release.ID, ActionID: action.ID, HostGroup: action.HostGroup, Claim: claim})
			}
		}
	}
	return append(issues, domain.ResourceConflicts(instances)...)
}

func (b *PlanBuilder) verifyRunResources(ctx context.Context, run domain.Run, plan lockedPlan) error {
	if plan.ResourcePolicyVersion == 0 {
		return nil
	}
	env, err := b.store.GetEnvironment(ctx, run.EnvironmentID, true)
	if err != nil {
		return err
	}
	revision, err := b.store.GetEnvironmentRevision(ctx, run.EnvironmentRevisionID)
	if err != nil {
		return err
	}
	if env.CurrentRevisionID != revision.ID {
		return fmt.Errorf("%w: environment configuration changed after the job was locked", domain.ErrConflict)
	}
	env.Revision = &revision
	return validateResourcePlan(ctx, b.store, env, &plan, run.ID, run.RetryOfRunID, run.RetryRootRunID)
}
