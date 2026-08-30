package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/sshcheck"
	"codex/platform-demo/internal/store"
)

type InventoryHost struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Groups  []string `json:"groups"`
	Port    int      `json:"port,omitempty"`
	User    string   `json:"user,omitempty"`
}

type InventoryDocument struct {
	Hosts []InventoryHost `json:"hosts"`
}

type EnvironmentLifecycle struct {
	RevisionCount         int  `json:"revisionCount"`
	RunCount              int  `json:"runCount"`
	ActiveRunCount        int  `json:"activeRunCount"`
	ImageBuildCount       int  `json:"imageBuildCount"`
	ActiveImageBuildCount int  `json:"activeImageBuildCount"`
	InstallationCount     int  `json:"installationCount"`
	Archived              bool `json:"archived"`
	CanDelete             bool `json:"canDelete"`
	CanArchive            bool `json:"canArchive"`
}

func (p *Platform) CreateEnvironment(ctx context.Context, user domain.User, environment domain.Environment, facts map[string]any) (domain.Environment, error) {
	if err := domain.ValidateRole(user, domain.RoleEnvironmentOwner); err != nil {
		return environment, err
	}
	if err := rejectSensitiveMap(facts, "environment fact"); err != nil {
		return environment, err
	}
	if strings.TrimSpace(environment.Name) == "" {
		return environment, fmt.Errorf("%w: environment name is required", domain.ErrInvalid)
	}
	now := time.Now().UTC()
	environment.ID, environment.OwnerID, environment.CreatedAt, environment.UpdatedAt = newID("environment"), user.ID, now, now
	inventory, _ := json.Marshal(InventoryDocument{Hosts: []InventoryHost{}})
	revision := domain.EnvironmentRevision{
		ID: newID("environment-revision"), EnvironmentID: environment.ID, Revision: 1,
		Facts: facts, Inventory: inventory, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{},
		CreatedBy: user.ID, ChangeReason: "创建环境", CreatedAt: now,
	}
	environment.CurrentRevisionID, environment.Revision = revision.ID, &revision
	if err := p.store.CreateEnvironment(ctx, environment, revision); err != nil {
		return environment, err
	}
	p.audit(ctx, user, "environment.created", "environment", environment.ID, map[string]any{"revisionId": revision.ID})
	return environment, nil
}

func (p *Platform) ListEnvironments(ctx context.Context, user domain.User, includeArchived ...bool) ([]domain.Environment, error) {
	include := len(includeArchived) > 0 && includeArchived[0] && user.Role == domain.RoleEnvironmentOwner
	environments, err := p.store.ListEnvironments(ctx, include)
	if err != nil {
		return nil, err
	}
	if include {
		visible := environments[:0]
		for _, environment := range environments {
			if environment.ArchivedAt == nil || environment.OwnerID == user.ID {
				visible = append(visible, environment)
			}
		}
		environments = visible
	}
	for i := range environments {
		if environments[i].Revision != nil {
			privileged := user.Role == domain.RoleEnvironmentOwner && user.ID == environments[i].OwnerID
			environments[i].Revision.CredentialRefs = domain.RedactCredentialRefs(environments[i].Revision.CredentialRefs, privileged)
			for revisionIndex := range environments[i].Revisions {
				environments[i].Revisions[revisionIndex].CredentialRefs = domain.RedactCredentialRefs(environments[i].Revisions[revisionIndex].CredentialRefs, privileged)
			}
		}
	}
	return environments, nil
}

func environmentLifecycleFromImpact(environment domain.Environment, impact store.EnvironmentLifecycleImpact) EnvironmentLifecycle {
	return EnvironmentLifecycle{
		RevisionCount: impact.RevisionCount, RunCount: impact.RunCount, ActiveRunCount: impact.ActiveRunCount,
		ImageBuildCount: impact.ImageBuildCount, ActiveImageBuildCount: impact.ActiveImageBuildCount, InstallationCount: impact.InstallationCount,
		Archived:   environment.ArchivedAt != nil,
		CanDelete:  impact.RunCount == 0 && impact.ImageBuildCount == 0 && impact.InstallationCount == 0,
		CanArchive: environment.ArchivedAt == nil && impact.ActiveRunCount == 0 && impact.ActiveImageBuildCount == 0 && impact.InstallationCount == 0,
	}
}

func (p *Platform) GetEnvironmentLifecycle(ctx context.Context, user domain.User, environmentID string) (EnvironmentLifecycle, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return EnvironmentLifecycle{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return EnvironmentLifecycle{}, err
	}
	impact, err := p.store.EnvironmentLifecycleImpact(ctx, environmentID)
	if err != nil {
		return EnvironmentLifecycle{}, err
	}
	return environmentLifecycleFromImpact(environment, impact), nil
}

func (p *Platform) DeleteEnvironment(ctx context.Context, user domain.User, environmentID string) error {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return err
	}
	impact, err := p.store.EnvironmentLifecycleImpact(ctx, environmentID)
	if err != nil {
		return err
	}
	if impact.InstallationCount > 0 {
		base := fmt.Errorf("%w: environment has %d installation baseline(s)", domain.ErrConflict, impact.InstallationCount)
		return actionableExistingError(base, "environment.installations_present", "该环境仍有组件安装基线，必须先回滚至干净状态", "一键回滚至干净状态", "/environments?selected="+environment.ID+"&action=rollback")
	}
	if impact.RunCount > 0 {
		base := fmt.Errorf("%w: environment is retained by %d run(s)", domain.ErrConflict, impact.RunCount)
		return actionableExistingError(base, "environment.run_history", "该环境已有运行记录，必须保留环境与 Revision 快照；可以改为归档", "查看运行记录", "/runs")
	}
	if impact.ImageBuildCount > 0 {
		base := fmt.Errorf("%w: environment is retained by %d image build(s)", domain.ErrConflict, impact.ImageBuildCount)
		return actionableExistingError(base, "environment.build_history", "该环境已有镜像构建记录，必须保留环境与 Revision 快照；可以改为归档", "查看组件构建记录", "/components")
	}
	audit := newAuditEvent(user, "environment.deleted", "environment", environment.ID, map[string]any{
		"name": environment.Name, "revisionCount": impact.RevisionCount,
	})
	if err := p.store.DeleteEnvironment(ctx, environment.ID, audit); err != nil {
		return err
	}
	p.hub.Publish("environment.deleted", map[string]any{"environmentId": environment.ID})
	return nil
}

func (p *Platform) ArchiveEnvironment(ctx context.Context, user domain.User, environmentID string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return environment, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return environment, err
	}
	if environment.ArchivedAt != nil {
		return environment, fmt.Errorf("%w: environment is already archived", domain.ErrConflict)
	}
	impact, err := p.store.EnvironmentLifecycleImpact(ctx, environmentID)
	if err != nil {
		return environment, err
	}
	if impact.ActiveRunCount > 0 {
		base := fmt.Errorf("%w: environment has %d active run(s)", domain.ErrConflict, impact.ActiveRunCount)
		return environment, actionableExistingError(base, "environment.active_runs", "该环境仍有活动 Run，必须等待结束或取消后再归档", "查看运行记录", "/runs")
	}
	if impact.ActiveImageBuildCount > 0 {
		base := fmt.Errorf("%w: environment has %d active image build(s)", domain.ErrConflict, impact.ActiveImageBuildCount)
		return environment, actionableExistingError(base, "environment.active_image_builds", "该环境仍有排队或执行中的镜像构建，必须等待结束后再归档", "查看组件构建记录", "/components")
	}
	if impact.InstallationCount > 0 {
		base := fmt.Errorf("%w: environment has %d installation baseline(s)", domain.ErrConflict, impact.InstallationCount)
		return environment, actionableExistingError(base, "environment.installations_present", "归档会禁止新的回滚操作，请先把环境回滚至干净状态", "一键回滚至干净状态", "/environments?selected="+environment.ID+"&action=rollback")
	}
	now := time.Now().UTC()
	audit := newAuditEvent(user, "environment.archived", "environment", environment.ID, map[string]any{
		"name": environment.Name, "runCount": impact.RunCount, "imageBuildCount": impact.ImageBuildCount,
	})
	if err := p.store.ArchiveEnvironment(ctx, environment.ID, now, audit); err != nil {
		return environment, err
	}
	environment.ArchivedAt, environment.UpdatedAt = &now, now
	p.hub.Publish("environment.archived", map[string]any{"environmentId": environment.ID})
	return environment, nil
}

func (p *Platform) UnarchiveEnvironment(ctx context.Context, user domain.User, environmentID string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return environment, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return environment, err
	}
	if environment.ArchivedAt == nil {
		return environment, fmt.Errorf("%w: environment is not archived", domain.ErrConflict)
	}
	now := time.Now().UTC()
	audit := newAuditEvent(user, "environment.unarchived", "environment", environment.ID, map[string]any{"name": environment.Name})
	if err := p.store.UnarchiveEnvironment(ctx, environment.ID, now, audit); err != nil {
		return environment, err
	}
	environment.ArchivedAt, environment.UpdatedAt = nil, now
	p.hub.Publish("environment.unarchived", map[string]any{"environmentId": environment.ID})
	return environment, nil
}

func ensureEnvironmentActive(environment domain.Environment) error {
	if environment.ArchivedAt != nil {
		return archivedEnvironmentError(environment.ID)
	}
	return nil
}

func archivedEnvironmentError(environmentID string) error {
	return actionableExistingError(fmt.Errorf("%w: environment is archived", domain.ErrConflict), "environment.archived", "该环境已归档，不能再创建 Revision、健康检查、构建或 Run", "恢复环境", "/environments?selected="+environmentID)
}

func (p *Platform) UpdateInventory(ctx context.Context, user domain.User, environmentID string, hosts []InventoryHost, changeReason ...string) (domain.Environment, error) {
	if len(hosts) > 256 {
		return domain.Environment{}, fmt.Errorf("%w: inventory supports at most 256 hosts", domain.ErrInvalid)
	}
	for _, host := range hosts {
		if strings.TrimSpace(host.Name) == "" || strings.TrimSpace(host.Address) == "" {
			return domain.Environment{}, fmt.Errorf("%w: every inventory host requires a name and address", domain.ErrInvalid)
		}
		if len(host.Groups) == 0 {
			return domain.Environment{}, fmt.Errorf("%w: inventory host %q requires at least one group", domain.ErrInvalid, host.Name)
		}
	}
	document, _ := json.Marshal(InventoryDocument{Hosts: hosts})
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		revision.Inventory = document
		return nil
	}, "environment.inventory_updated", firstReason(changeReason))
}

func (p *Platform) UpdateEnvironmentFacts(ctx context.Context, user domain.User, environmentID string, facts map[string]any, changeReason ...string) (domain.Environment, error) {
	if err := rejectSensitiveMap(facts, "environment fact"); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		revision.Facts = facts
		return nil
	}, "environment.facts_updated", firstReason(changeReason))
}

var environmentVariablePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func normalizeEnvironmentVariables(variables map[string]string, refs []domain.CredentialRef) (map[string]string, error) {
	credentialNames := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		credentialNames[ref.Name] = struct{}{}
	}
	keys := make([]string, 0, len(variables))
	for name := range variables {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	normalized := make(map[string]string, len(variables))
	for _, name := range keys {
		if !environmentVariablePattern.MatchString(name) {
			return nil, fmt.Errorf("%w: environment variable %q must be an uppercase identifier", domain.ErrInvalid, name)
		}
		if isSensitiveKey(name) {
			return nil, fmt.Errorf("%w: sensitive environment variable %q must use a CredentialRef", domain.ErrInvalid, name)
		}
		if _, exists := credentialNames[name]; exists {
			return nil, fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", domain.ErrInvalid, name)
		}
		value := variables[name]
		if name == imageRegistryVariable {
			registry, err := normalizeImageRegistry(value)
			if err != nil {
				return nil, err
			}
			value = registry
		}
		normalized[name] = value
	}
	return normalized, nil
}

func (p *Platform) UpdateEnvironmentVariables(ctx context.Context, user domain.User, environmentID string, variables map[string]string, changeReason ...string) (domain.Environment, error) {
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		normalized, err := normalizeEnvironmentVariables(variables, revision.CredentialRefs)
		if err != nil {
			return err
		}
		revision.Variables = normalized
		return nil
	}, "environment.variables_updated", firstReason(changeReason))
}

func rejectSensitiveMap(values map[string]any, label string) error {
	if path, ok := findSensitiveParameter(values, ""); ok {
		return fmt.Errorf("%w: sensitive %s %q must use a CredentialRef", domain.ErrInvalid, label, path)
	}
	return nil
}

func findSensitiveParameter(parameters map[string]any, prefix string) (string, bool) {
	return findSensitiveValue(parameters, prefix)
}

func findSensitiveValue(value any, prefix string) (string, bool) {
	switch values := value.(type) {
	case map[string]any:
		for key, child := range values {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if isSensitiveKey(key) {
				return path, true
			}
			if nested, found := findSensitiveValue(child, path); found {
				return nested, true
			}
		}
	case []any:
		for index, child := range values {
			path := fmt.Sprintf("%s[%d]", prefix, index)
			if nested, found := findSensitiveValue(child, path); found {
				return nested, true
			}
		}
	}
	return "", false
}

func (p *Platform) UpdateCredentialRefs(ctx context.Context, user domain.User, environmentID string, refs []domain.CredentialRef, changeReason ...string) (domain.Environment, error) {
	if err := ValidateCredentialRefs(refs); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		if _, err := normalizeEnvironmentVariables(revision.Variables, refs); err != nil {
			return err
		}
		revision.CredentialRefs = refs
		return nil
	}, "environment.credentials_updated", firstReason(changeReason))
}

func firstReason(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return strings.TrimSpace(reasons[0])
}

func (p *Platform) updateEnvironmentRevision(ctx context.Context, user domain.User, environmentID string, mutate func(*domain.EnvironmentRevision) error, auditAction, changeReason string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return environment, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return environment, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return environment, err
	}
	if environment.Revision == nil {
		return environment, fmt.Errorf("%w: environment has no current revision", domain.ErrConflict)
	}
	revision := *environment.Revision
	revision.CredentialRefs = append([]domain.CredentialRef(nil), revision.CredentialRefs...)
	revision.Facts = cloneMap(revision.Facts)
	revision.Variables = cloneStringMap(revision.Variables)
	if err := mutate(&revision); err != nil {
		return environment, err
	}
	next, err := p.store.NextEnvironmentRevision(ctx, environmentID)
	if err != nil {
		return environment, err
	}
	revision.ID, revision.Revision, revision.CreatedAt = newID("environment-revision"), next, time.Now().UTC()
	revision.CreatedBy, revision.ChangeReason = user.ID, strings.TrimSpace(changeReason)
	if err := p.store.CreateEnvironmentRevision(ctx, revision); err != nil {
		return environment, err
	}
	environment.CurrentRevisionID, environment.Revision, environment.UpdatedAt = revision.ID, &revision, revision.CreatedAt
	p.audit(ctx, user, auditAction, "environment", environmentID, map[string]any{"revisionId": revision.ID, "revision": next, "changeReason": revision.ChangeReason})
	return environment, nil
}

func (p *Platform) RestoreEnvironmentRevision(ctx context.Context, user domain.User, environmentID, revisionID, changeReason string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return environment, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return environment, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return environment, err
	}
	target, err := p.store.GetEnvironmentRevision(ctx, revisionID)
	if err != nil {
		return environment, err
	}
	if target.EnvironmentID != environmentID {
		return environment, fmt.Errorf("%w: revision does not belong to environment", domain.ErrInvalid)
	}
	reason := strings.TrimSpace(changeReason)
	if reason == "" {
		return environment, fmt.Errorf("%w: change reason is required", domain.ErrInvalid)
	}
	next, err := p.store.NextEnvironmentRevision(ctx, environmentID)
	if err != nil {
		return environment, err
	}
	restored := target
	restored.ID, restored.Revision, restored.CreatedAt = newID("environment-revision"), next, time.Now().UTC()
	restored.CreatedBy, restored.ChangeReason = user.ID, reason
	restored.Inventory = append([]byte(nil), target.Inventory...)
	restored.Facts = cloneMap(target.Facts)
	restored.Variables = cloneStringMap(target.Variables)
	restored.CredentialRefs = append([]domain.CredentialRef(nil), target.CredentialRefs...)
	if err := p.store.CreateEnvironmentRevision(ctx, restored); err != nil {
		return environment, err
	}
	environment.CurrentRevisionID, environment.Revision, environment.UpdatedAt = restored.ID, &restored, restored.CreatedAt
	p.audit(ctx, user, "environment.revision_restored", "environment", environmentID, map[string]any{"revisionId": restored.ID, "revision": next, "sourceRevisionId": revisionID, "sourceRevision": target.Revision, "changeReason": reason})
	return environment, nil
}

func (p *Platform) CheckEnvironmentHealth(ctx context.Context, user domain.User, environmentID string) (domain.EnvironmentHealthCheck, error) {
	environment, err := p.loadEnvironmentConnectivityTarget(ctx, user, environmentID)
	if err != nil {
		return domain.EnvironmentHealthCheck{}, err
	}
	return p.checkEnvironmentHealth(ctx, user, environment)
}

func (p *Platform) loadEnvironmentConnectivityTarget(ctx context.Context, user domain.User, environmentID string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return domain.Environment{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return domain.Environment{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return domain.Environment{}, err
	}
	if environment.Revision == nil {
		return domain.Environment{}, fmt.Errorf("%w: environment has no current revision", domain.ErrConflict)
	}
	return environment, nil
}

func (p *Platform) checkEnvironmentHealth(ctx context.Context, user domain.User, environment domain.Environment) (domain.EnvironmentHealthCheck, error) {
	checkContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var inventory InventoryDocument
	if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil {
		return domain.EnvironmentHealthCheck{}, fmt.Errorf("%w: invalid environment inventory", domain.ErrInvalid)
	}
	targets := make([]domain.EnvironmentEndpointCheck, 0, len(inventory.Hosts)+2)
	for _, host := range inventory.Hosts {
		port := host.Port
		if port == 0 {
			port = 22
		}
		targets = append(targets, domain.EnvironmentEndpointCheck{Kind: "host", Name: host.Name, Address: net.JoinHostPort(host.Address, fmt.Sprint(port))})
	}
	for _, name := range []string{"IMAGE_REGISTRY", "FILE_STATION"} {
		if value := strings.TrimSpace(environment.Revision.Variables[name]); value != "" {
			address := strings.SplitN(value, "/", 2)[0]
			targets = append(targets, domain.EnvironmentEndpointCheck{Kind: "dependency", Name: name, Address: address})
		}
	}
	if len(inventory.Hosts) == 0 {
		targets = append(targets, domain.EnvironmentEndpointCheck{Kind: "configuration", Name: "Inventory", Address: "—", Error: "尚未配置主机"})
	}
	results := make([]domain.EnvironmentEndpointCheck, len(targets))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if target.Kind == "configuration" {
				results[index] = target
				return
			}
			if _, _, splitErr := net.SplitHostPort(target.Address); splitErr != nil {
				target.Error = "地址必须包含有效端口"
				results[index] = target
				return
			}
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			started := time.Now()
			conn, dialErr := p.dialContext(checkContext, "tcp", target.Address)
			target.LatencyMS = time.Since(started).Milliseconds()
			if dialErr != nil {
				target.Error = "TCP 连接失败"
			} else {
				target.Reachable = true
				_ = conn.Close()
			}
			results[index] = target
		}()
	}
	wg.Wait()
	status := "healthy"
	for _, result := range results {
		if !result.Reachable {
			status = "degraded"
			break
		}
	}
	check := domain.EnvironmentHealthCheck{
		ID: newID("environment-health"), EnvironmentID: environment.ID, EnvironmentRevisionID: environment.Revision.ID,
		Status: status, Results: results, CheckedAt: time.Now().UTC(),
	}
	if err := p.store.SaveEnvironmentHealthCheck(ctx, check); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			archived, stateErr := p.store.EnvironmentArchived(ctx, environment.ID)
			if stateErr == nil && archived {
				return domain.EnvironmentHealthCheck{}, archivedEnvironmentError(environment.ID)
			}
		}
		return domain.EnvironmentHealthCheck{}, err
	}
	reachable := 0
	for _, result := range results {
		if result.Reachable {
			reachable++
		}
	}
	p.audit(ctx, user, "environment.health_checked", "environment", environment.ID, map[string]any{"revisionId": environment.Revision.ID, "status": status, "reachable": reachable, "total": len(results)})
	return check, nil
}

var environmentSSHCredentialNames = struct {
	password   []string
	privateKey []string
}{
	password:   []string{"ssh_password", "ansible_ssh_pass", "ansible_password"},
	privateKey: []string{"ssh_private_key", "ansible_private_key_file", "ansible_ssh_private_key_file"},
}

type environmentSSHCredentials struct {
	password       string
	privateKeyPath string
}

func (p *Platform) CheckEnvironmentConnectivity(ctx context.Context, user domain.User, environmentID string) (domain.EnvironmentConnectivityCheck, error) {
	environment, err := p.loadEnvironmentConnectivityTarget(ctx, user, environmentID)
	if err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	tcpCheck, err := p.checkEnvironmentHealth(ctx, user, environment)
	if err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	sshCheck, err := p.checkEnvironmentSSH(ctx, user, environment)
	if err != nil {
		return domain.EnvironmentConnectivityCheck{}, err
	}
	return domain.EnvironmentConnectivityCheck{TCP: tcpCheck, SSH: sshCheck}, nil
}

func (p *Platform) CheckEnvironmentSSH(ctx context.Context, user domain.User, environmentID string) (domain.EnvironmentSSHCheck, error) {
	environment, err := p.loadEnvironmentConnectivityTarget(ctx, user, environmentID)
	if err != nil {
		return domain.EnvironmentSSHCheck{}, err
	}
	return p.checkEnvironmentSSH(ctx, user, environment)
}

func (p *Platform) checkEnvironmentSSH(ctx context.Context, user domain.User, environment domain.Environment) (domain.EnvironmentSSHCheck, error) {
	started := time.Now()
	checkContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var inventory InventoryDocument
	if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil {
		return domain.EnvironmentSSHCheck{}, fmt.Errorf("%w: invalid environment inventory", domain.ErrInvalid)
	}
	if len(inventory.Hosts) == 0 {
		return p.saveEnvironmentSSHCheck(ctx, user, environment, started, []domain.EnvironmentSSHHostCheck{{Kind: "configuration", Name: "Inventory", Address: "—", Status: "failed", ErrorCode: "inventory_empty", Message: "尚未配置可检查的 Inventory 主机"}})
	}

	remoteHosts := make([]InventoryHost, 0, len(inventory.Hosts))
	for _, host := range inventory.Hosts {
		if localInventoryAddress(host.Address) {
			continue
		}
		remoteHosts = append(remoteHosts, host)
	}
	if len(remoteHosts) == 0 {
		results := make([]domain.EnvironmentSSHHostCheck, 0, len(inventory.Hosts))
		for _, host := range inventory.Hosts {
			results = append(results, sshCheckTarget(host, "skipped", "local_connection", "本地主机使用 local connection，不需要 SSH"))
		}
		return p.saveEnvironmentSSHCheck(ctx, user, environment, started, results)
	}

	credentials, credentialCode, credentialMessage := resolveEnvironmentSSHCredentials(environment.Revision.CredentialRefs)
	if credentialCode != "" {
		return p.saveEnvironmentSSHCheck(ctx, user, environment, started, []domain.EnvironmentSSHHostCheck{{Kind: "configuration", Name: "SSH CredentialRef", Address: "—", Status: "failed", ErrorCode: credentialCode, Message: credentialMessage}})
	}
	if p.sshChecker == nil {
		return p.saveEnvironmentSSHCheck(ctx, user, environment, started, []domain.EnvironmentSSHHostCheck{{Kind: "configuration", Name: "Go SSH Checker", Address: "—", Status: "failed", ErrorCode: "ssh_checker_unavailable", Message: "平台未配置 Go SSH 检查器"}})
	}

	results := make([]domain.EnvironmentSSHHostCheck, len(inventory.Hosts))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, host := range inventory.Hosts {
		if localInventoryAddress(host.Address) {
			results[index] = sshCheckTarget(host, "skipped", "local_connection", "本地主机使用 local connection，不需要 SSH")
			continue
		}
		if strings.TrimSpace(host.User) == "" {
			results[index] = sshCheckTarget(host, "failed", "ssh_user_missing", "Inventory 主机必须明确配置 SSH 用户")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			hostContext, hostCancel := context.WithTimeout(checkContext, 10*time.Second)
			defer hostCancel()
			port := host.Port
			if port == 0 {
				port = 22
			}
			err := p.sshChecker.Check(hostContext, sshcheck.Request{
				Address: net.JoinHostPort(host.Address, fmt.Sprint(port)), User: host.User,
				KnownHostsPath: p.sshKnownHostsPath, PrivateKeyPath: credentials.privateKeyPath, Password: credentials.password,
			})
			if err == nil {
				results[index] = sshCheckTarget(host, "passed", "", "")
				return
			}
			status, code, message := classifyGoSSHFailure(err)
			results[index] = sshCheckTarget(host, status, code, message)
		}()
	}
	wg.Wait()
	return p.saveEnvironmentSSHCheck(ctx, user, environment, started, results)
}

func localInventoryAddress(address string) bool {
	return address == "localhost" || address == "127.0.0.1" || address == "::1"
}

func sshCheckTarget(host InventoryHost, status, code, message string) domain.EnvironmentSSHHostCheck {
	port := host.Port
	if port == 0 {
		port = 22
	}
	return domain.EnvironmentSSHHostCheck{Kind: "host", Name: host.Name, Address: net.JoinHostPort(host.Address, fmt.Sprint(port)), User: host.User, Status: status, ErrorCode: code, Message: message}
}

func resolveEnvironmentSSHCredentials(refs []domain.CredentialRef) (environmentSSHCredentials, string, string) {
	var credentials environmentSSHCredentials
	privateKeyRef := preferredCredentialRef(refs, environmentSSHCredentialNames.privateKey)
	passwordRef := preferredCredentialRef(refs, environmentSSHCredentialNames.password)
	if privateKeyRef == nil && passwordRef == nil {
		return credentials, "ssh_credential_missing", "必须显式配置 ssh_private_key 或 ssh_password CredentialRef"
	}
	if privateKeyRef != nil {
		switch {
		case privateKeyRef.Kind == "sshKeyPath" && strings.TrimSpace(privateKeyRef.Reference) != "":
			credentials.privateKeyPath = privateKeyRef.Reference
		case privateKeyRef.Name != "ssh_private_key" && privateKeyRef.Kind == "envVarRef" && strings.TrimSpace(privateKeyRef.Reference) != "":
			path, ok := os.LookupEnv(privateKeyRef.Reference)
			if !ok || strings.TrimSpace(path) == "" {
				return credentials, "ssh_credential_unconfigured", "旧名称 SSH 私钥 CredentialRef 的后端环境变量未配置"
			}
			credentials.privateKeyPath = path
		default:
			return credentials, "ssh_credential_unconfigured", "ssh_private_key 必须使用 sshKeyPath 并引用绝对路径"
		}
	}
	if passwordRef != nil {
		if passwordRef.Kind != "envVarRef" || strings.TrimSpace(passwordRef.Reference) == "" {
			return credentials, "ssh_credential_unconfigured", "SSH 密码 CredentialRef 必须使用 envVarRef"
		}
		password, ok := os.LookupEnv(passwordRef.Reference)
		if !ok || password == "" {
			return credentials, "ssh_credential_unconfigured", "SSH 密码 CredentialRef 的后端环境变量未配置"
		}
		credentials.password = password
	}
	return credentials, "", ""
}

func preferredCredentialRef(refs []domain.CredentialRef, names []string) *domain.CredentialRef {
	for _, name := range names {
		for index := range refs {
			if refs[index].Name == name {
				return &refs[index]
			}
		}
	}
	return nil
}

func classifyGoSSHFailure(err error) (string, string, string) {
	var checkErr *sshcheck.CheckError
	if !errors.As(err, &checkErr) {
		return "failed", "ssh_handshake_failed", "Go SSH 检查失败"
	}
	switch checkErr.Kind {
	case sshcheck.ErrorPrivateKey:
		return "failed", "ssh_private_key_invalid", "SSH 私钥不存在、不可读、已加密或格式无效"
	case sshcheck.ErrorKnownHosts:
		return "failed", "ssh_known_hosts_unconfigured", "平台 known_hosts 文件未配置或不可读"
	case sshcheck.ErrorHostKey:
		return "unreachable", "ssh_host_key_failed", "SSH 主机指纹未知或与 known_hosts 不一致"
	case sshcheck.ErrorAuthentication:
		return "unreachable", "ssh_authentication_failed", "SSH 用户或凭据认证失败"
	case sshcheck.ErrorNetwork:
		message := strings.ToLower(checkErr.Error())
		if strings.Contains(message, "connection refused") {
			return "unreachable", "ssh_connection_refused", "SSH 端口拒绝连接，请检查 sshd 和端口配置"
		}
		return "unreachable", "ssh_network_unreachable", "SSH 网络不可达"
	case sshcheck.ErrorTimeout:
		return "unreachable", "ssh_check_timeout", "单台主机 SSH 检查超过 10 秒"
	case sshcheck.ErrorCommand:
		return "failed", "ssh_command_failed", "SSH 已认证，但创建会话或执行 true 失败"
	default:
		return "failed", "ssh_handshake_failed", "SSH 协议握手或会话创建失败"
	}
}

func (p *Platform) saveEnvironmentSSHCheck(ctx context.Context, user domain.User, environment domain.Environment, started time.Time, results []domain.EnvironmentSSHHostCheck) (domain.EnvironmentSSHCheck, error) {
	status := "healthy"
	passed := 0
	for _, result := range results {
		if result.Status == "passed" || result.Status == "skipped" {
			passed++
			continue
		}
		status = "degraded"
	}
	check := domain.EnvironmentSSHCheck{
		ID: newID("environment-ssh"), EnvironmentID: environment.ID, EnvironmentRevisionID: environment.Revision.ID,
		Status: status, DurationMS: time.Since(started).Milliseconds(), Results: results, CheckedAt: time.Now().UTC(),
	}
	if err := p.store.SaveEnvironmentSSHCheck(ctx, check); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			archived, stateErr := p.store.EnvironmentArchived(ctx, environment.ID)
			if stateErr == nil && archived {
				return domain.EnvironmentSSHCheck{}, archivedEnvironmentError(environment.ID)
			}
		}
		return domain.EnvironmentSSHCheck{}, err
	}
	p.audit(ctx, user, "environment.ssh_checked", "environment", environment.ID, map[string]any{"revisionId": environment.Revision.ID, "status": status, "passed": passed, "total": len(results)})
	return check, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return deepCopy(input).(map[string]any)
}
