package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

// Installation Runs select the groups; the current inventory selects their
// members. Shared service endpoints identify protected hosts, not extra targets.
type environmentResetState struct {
	installations []domain.EnvironmentComponentInstallation
	targetHosts   []domain.RunInventoryHost
	baselineHosts map[string]map[string]bool
	boundary      *resetBoundary
}

type resetBoundary struct {
	inventory InventoryDocument
	protected map[string]bool
	addresses map[string][]string
}

func newResetBoundary(ctx context.Context, revision domain.EnvironmentRevision) (*resetBoundary, error) {
	b := &resetBoundary{protected: map[string]bool{}, addresses: map[string][]string{}}
	if err := json.Unmarshal(revision.Inventory, &b.inventory); err != nil {
		return nil, fmt.Errorf("%w: 无法读取重置目标 Inventory", domain.ErrConflict)
	}
	for _, name := range []string{"FILE_STATION", "IMAGE_REGISTRY"} {
		value := strings.TrimSpace(revision.Variables[name])
		if value == "" {
			continue
		}
		host, _, err := net.SplitHostPort(strings.SplitN(value, "/", 2)[0])
		if err != nil {
			// A registry prefix can omit its default port.
			host = strings.SplitN(value, "/", 2)[0]
			if strings.Contains(host, ":") {
				return nil, fmt.Errorf("%w: %s 地址无法确定，不能校验共享服务保护边界", domain.ErrConflict, name)
			}
		}
		addresses, err := b.resolve(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", domain.ErrConflict, name, err)
		}
		for _, address := range addresses {
			b.protected[address] = true
		}
	}
	return b, nil
}

func (b *resetBoundary) resolve(ctx context.Context, host string) ([]string, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if addresses, ok := b.addresses[host]; ok {
		return addresses, nil
	}
	addresses := []string{}
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = append(addresses, ip.Unmap().String())
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("无法解析主机 %q，不能确定重置边界", host)
		}
		seen := map[string]bool{}
		for _, ip := range ips {
			address := ip.Unmap().String()
			if !seen[address] {
				addresses = append(addresses, address)
				seen[address] = true
			}
		}
	}
	sort.Strings(addresses)
	b.addresses[host] = addresses
	return addresses, nil
}

func resetGroupHosts(inventory InventoryDocument, group string) ([]domain.RunInventoryHost, error) {
	if group == "" || !inventoryToken(group) {
		return nil, fmt.Errorf("%w: 来源 Run 未锁定明确主机组 %q", domain.ErrConflict, group)
	}
	hosts := []domain.RunInventoryHost{}
	for _, host := range inventory.Hosts {
		include := group == "all" || group == host.Name
		for _, g := range host.Groups {
			include = include || g == group
		}
		if include {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: 来源 Run 主机组 %q 没有可确认的目标节点", domain.ErrConflict, group)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	return hosts, nil
}

func resetHostIdentity(host domain.RunInventoryHost) string {
	port := host.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", host.Name, strings.ToLower(strings.TrimSuffix(host.Address, ".")), port, host.User)
}

// A mixed group cannot be narrowed without changing the original restoration
// contract. An exclusively shared-service group is retained in its entirety.
func (b *resetBoundary) classify(ctx context.Context, hosts []domain.RunInventoryHost) (bool, error) {
	shared, cluster := false, false
	for _, host := range hosts {
		addresses, err := b.resolve(ctx, host.Address)
		if err != nil {
			return false, fmt.Errorf("%w: %v", domain.ErrConflict, err)
		}
		for _, address := range addresses {
			if b.protected[address] {
				shared = true
			} else {
				cluster = true
			}
		}
	}
	if shared && cluster {
		return false, fmt.Errorf("%w: 来源 Run 的目标同时包含集群节点和共享服务，不能重置混合主机组", domain.ErrConflict)
	}
	return shared, nil
}

func (r *RollbackPlanner) environmentResetState(ctx context.Context, revision domain.EnvironmentRevision) (environmentResetState, error) {
	state := environmentResetState{installations: []domain.EnvironmentComponentInstallation{}, targetHosts: []domain.RunInventoryHost{}, baselineHosts: map[string]map[string]bool{}}
	installations, err := r.recoveryBaselines(ctx, revision.EnvironmentID)
	if err != nil {
		return state, err
	}
	// An empty record set is an ordinary empty preview, not proof of host health.
	if len(installations) == 0 {
		return state, nil
	}
	state.boundary, err = newResetBoundary(ctx, revision)
	if err != nil {
		return state, err
	}
	targets := map[string]domain.RunInventoryHost{}
	for _, installation := range installations {
		source, err := r.store.GetRun(ctx, installation.InstallRunID)
		if err != nil || source.EnvironmentID != revision.EnvironmentID {
			return state, fmt.Errorf("%w: 无法确认组件 %s 的来源 Run", domain.ErrConflict, installation.ComponentID)
		}
		plan, err := planFromRun(source)
		if err != nil {
			return state, fmt.Errorf("%w: 来源 Run %s 缺少锁定计划", domain.ErrConflict, source.ID)
		}
		sourceRevision, err := r.store.GetEnvironmentRevision(ctx, source.EnvironmentRevisionID)
		if err != nil {
			return state, err
		}
		if sourceRevision.EnvironmentID != revision.EnvironmentID {
			return state, fmt.Errorf("%w: 来源 Run %s 的环境版本不属于当前环境", domain.ErrConflict, source.ID)
		}
		sourceBoundary, err := newResetBoundary(ctx, sourceRevision)
		if err != nil {
			return state, err
		}
		matched, shared, cluster := false, false, false
		for _, step := range plan.Steps {
			if step.ComponentID != installation.ComponentID || step.SourceNodeID != installation.NodeID || step.ReleaseID != installation.ReleaseID || step.ActionID != installation.Backup.ActionID || (step.Action != domain.ActionInstall && step.Action != domain.ActionConfigure && step.Action != domain.ActionUpgrade) {
				continue
			}
			matched = true
			sourceHosts, err := resetGroupHosts(sourceBoundary.inventory, step.Limit)
			if err != nil {
				return state, err
			}
			wasShared, err := sourceBoundary.classify(ctx, sourceHosts)
			if err != nil {
				return state, err
			}
			hosts, err := resetGroupHosts(state.boundary.inventory, step.Limit)
			if err != nil {
				return state, err
			}
			isShared, err := state.boundary.classify(ctx, hosts)
			if err != nil {
				return state, err
			}
			if wasShared != isShared {
				return state, fmt.Errorf("%w: 来源 Run %s 的集群节点与当前共享服务边界冲突", domain.ErrConflict, source.ID)
			}
			shared, cluster = shared || isShared, cluster || !isShared
			if isShared {
				continue
			}
			for _, host := range hosts {
				targets[host.Name] = host
				key := installation.ComponentID + "/" + installation.NodeID
				if state.baselineHosts[key] == nil {
					state.baselineHosts[key] = map[string]bool{}
				}
				state.baselineHosts[key][resetHostIdentity(host)] = true
			}
		}
		if !matched || (shared && cluster) {
			return state, fmt.Errorf("%w: 组件 %s 的来源记录无法划定独立的集群恢复边界", domain.ErrConflict, installation.ComponentID)
		}
		if cluster {
			state.installations = append(state.installations, installation)
		}
	}
	for _, host := range targets {
		state.targetHosts = append(state.targetHosts, host)
	}
	sort.Slice(state.targetHosts, func(i, j int) bool { return state.targetHosts[i].Name < state.targetHosts[j].Name })
	return state, nil
}

// A continuation consisting only of an unfinished postcheck still owns its
// parent's recovery baseline, until that check has completed successfully.
func resetRecoverySteps(plan domain.RunExecutionPlan) []domain.RunPlanStep {
	steps := append([]domain.RunPlanStep{}, plan.Steps...)
	for _, step := range plan.Steps {
		if step.Phase == "post" || step.Phase == "pre" {
			if parent := parentExecutionStep(plan.ParentSteps, step); parent != nil {
				steps = append(steps, *parent)
			}
		}
	}
	return steps
}

func resetBoundaryDigest(ctx context.Context, revision domain.EnvironmentRevision, targets []domain.RunInventoryHost) (string, error) {
	b, err := newResetBoundary(ctx, revision)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		found := false
		for _, current := range b.inventory.Hosts {
			found = found || (resetHostIdentity(target) == resetHostIdentity(current) && digestValue(target.Groups) == digestValue(current.Groups))
		}
		if !found {
			return "", fmt.Errorf("%w: 重置目标 %s 已改变，请重新预览", domain.ErrConflict, target.Name)
		}
		shared, err := b.classify(ctx, []domain.RunInventoryHost{target})
		if err != nil {
			return "", err
		}
		if shared {
			return "", fmt.Errorf("%w: 重置目标 %s 与共享服务同机", domain.ErrConflict, target.Name)
		}
	}
	return digestValue(struct {
		Targets   []domain.RunInventoryHost
		Inventory InventoryDocument
		Addresses map[string][]string
		Protected map[string]bool
	}{targets, b.inventory, b.addresses, b.protected}), nil
}

func resetTaskTargetScope(targets []domain.RunInventoryHost) ansible.TaskTargetScope {
	scope := ansible.TaskTargetScope{Hosts: []string{}, Groups: map[string][]string{}}
	for _, host := range targets {
		scope.Hosts = append(scope.Hosts, host.Name)
	}
	return scope
}

func resetTaskTargetScopeWithInventory(targets []domain.RunInventoryHost, inventory InventoryDocument) ansible.TaskTargetScope {
	scope := resetTaskTargetScope(targets)
	for _, host := range inventory.Hosts {
		scope.Groups["all"] = append(scope.Groups["all"], host.Name)
		for _, group := range host.Groups {
			if group != "all" {
				scope.Groups[group] = append(scope.Groups[group], host.Name)
			}
		}
	}
	return scope
}

func (r *RollbackPlanner) validateEnvironmentReset(ctx context.Context, revision domain.EnvironmentRevision, plan domain.RunExecutionPlan, complete bool) error {
	boundaryDigest, err := resetBoundaryDigest(ctx, revision, plan.ResetTargets)
	if err != nil {
		return err
	}
	if len(plan.ResetTargets) == 0 || plan.ResetBoundaryDigest == "" || boundaryDigest != plan.ResetBoundaryDigest {
		return fmt.Errorf("%w: 集群重置目标或共享服务边界已改变，请重新预览", domain.ErrConflict)
	}
	state, err := r.environmentResetState(ctx, revision)
	if err != nil {
		return err
	}
	if complete {
		if len(state.installations) != 0 {
			return fmt.Errorf("%w: 集群仍有 %d 项安装基线或待恢复操作，重置未完成", domain.ErrConflict, len(state.installations))
		}
		return nil
	}
	if len(plan.ArtifactTransfers) != 0 || len(plan.ImageTransfers) != 0 || len(plan.DeliveryRequirements) != 0 {
		return fmt.Errorf("%w: 集群重置不能包含对 File Station 或镜像仓库的媒体交付", domain.ErrConflict)
	}
	expected := installationBaselineFromSteps(resetRecoverySteps(plan))
	if len(expected) == 0 || len(expected) != len(state.installations) {
		return fmt.Errorf("%w: 重置计划未覆盖全部待恢复的集群组件，请重新预览", domain.ErrConflict)
	}
	actual := make([]domain.RunInstallationBaseline, 0, len(state.installations))
	for _, installation := range state.installations {
		if installation.Backup.Previous != nil {
			return fmt.Errorf("%w: 集群存在多层恢复基线，无法直接重置", domain.ErrConflict)
		}
		actual = append(actual, domain.RunInstallationBaseline{NodeID: installation.NodeID, ComponentID: installation.ComponentID, ReleaseID: installation.ReleaseID, InstallRunID: installation.InstallRunID, BackupRef: installation.BackupRef, PlaybookSHA256: installation.Backup.PlaybookSHA256})
	}
	if installationBaselineDigest(actual) != installationBaselineDigest(expected) {
		return fmt.Errorf("%w: 集群恢复基线与锁定计划不一致，请重新预览", domain.ErrConflict)
	}
	coveredHosts := map[string]map[string]bool{}
	for _, step := range resetRecoverySteps(plan) {
		hosts, err := resetGroupHosts(state.boundary.inventory, step.Limit)
		if err != nil {
			return err
		}
		shared, err := state.boundary.classify(ctx, hosts)
		if err != nil {
			return err
		}
		if shared {
			return fmt.Errorf("%w: 重置步骤 %s 指向保留的共享服务", domain.ErrConflict, step.Name)
		}
		for _, host := range hosts {
			found := false
			for _, target := range state.targetHosts {
				found = found || resetHostIdentity(host) == resetHostIdentity(target)
			}
			if !found {
				return fmt.Errorf("%w: 重置步骤超出待恢复主机组的当前成员 %s", domain.ErrConflict, host.Name)
			}
			locked := false
			for _, target := range plan.ResetTargets {
				locked = locked || resetHostIdentity(host) == resetHostIdentity(target)
			}
			if !locked {
				return fmt.Errorf("%w: 重置计划未锁定目标节点 %s，请重新预览", domain.ErrConflict, host.Name)
			}
			if step.Backup != nil && (step.Action == domain.ActionRollback || step.Action == domain.ActionUninstall) {
				key := step.ComponentID + "/" + step.Backup.NodeID
				if !state.baselineHosts[key][resetHostIdentity(host)] {
					return fmt.Errorf("%w: 组件 %s 的重置步骤超出安装主机组的当前成员", domain.ErrConflict, step.ComponentID)
				}
				if coveredHosts[key] == nil {
					coveredHosts[key] = map[string]bool{}
				}
				coveredHosts[key][resetHostIdentity(host)] = true
			}
		}
	}
	if digestValue(coveredHosts) != digestValue(state.baselineHosts) {
		return fmt.Errorf("%w: 重置计划未覆盖全部安装目标主机，请重新预览", domain.ErrConflict)
	}
	playbooks := []string{}
	for _, step := range resetRecoverySteps(plan) {
		playbooks = append(playbooks, step.Playbook)
	}
	if err := r.inspector.ValidateTaskTargets(playbooks, resetTaskTargetScopeWithInventory(plan.ResetTargets, state.boundary.inventory)); err != nil {
		return fmt.Errorf("%w: 重置任务目标校验失败: %v", domain.ErrConflict, err)
	}
	return nil
}
