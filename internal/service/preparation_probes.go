package service

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/sshcheck"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

type preparationProbeKey struct{}
type preparationProbeObserver func(context.Context, domain.Environment, lockedPlan) error

func observePreparationPlan(ctx context.Context, environment domain.Environment, plan lockedPlan) error {
	if observer, ok := ctx.Value(preparationProbeKey{}).(preparationProbeObserver); ok {
		return observer(ctx, environment, plan)
	}
	return nil
}
func (s *EnvironmentService) probeForPreparation(ctx context.Context, environment domain.Environment, plan lockedPlan, report func(PreparationCheck)) error {
	var inventory InventoryDocument
	if err := json.Unmarshal(environment.Revision.Inventory, &inventory); err != nil {
		return err
	}
	credentials, _, credentialError := resolveEnvironmentSSHCredentials(environment.Revision.CredentialRefs)
	seen := map[string]bool{}
	failed := false
	probe := func(host InventoryHost, id, category string, check sshcheck.Probe) {
		key := host.Name + ":" + check.Kind + ":" + check.Target
		if seen[key] {
			return
		}
		seen[key] = true
		label := check.Kind + " · " + safePreparationError(fmt.Errorf("%s", check.Target))
		if check.RegistryTransportFromDocker {
			if endpoint, err := url.Parse(check.Target); err == nil {
				label = "镜像仓库 · " + endpoint.Host + "（按主机配置）"
			}
		}
		item := PreparationCheck{ID: id + ":" + host.Name, Category: category, Label: label, Host: host.Name, Status: "running", StartedAt: time.Now().UTC()}
		report(item)
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var result sshcheck.ProbeResult
		var err error
		if localInventoryAddress(host.Address) {
			command, e := sshcheck.ProbeCommand(check)
			if e != nil {
				err = e
			} else {
				output, e := exec.CommandContext(probeCtx, "sh", "-c", command).CombinedOutput()
				err = e
				result.Observed = string(output)
			}
		} else if credentialError != "" {
			err = fmt.Errorf("%s", credentialError)
		} else if s.sshChecker == nil {
			err = fmt.Errorf("SSH 检查器未配置")
		} else {
			port := host.Port
			if port == 0 {
				port = 22
			}
			result, err = s.sshChecker.Probe(probeCtx, sshcheck.Request{Address: net.JoinHostPort(host.Address, fmt.Sprint(port)), User: host.User, KnownHostsPath: s.sshKnownHostsPath, PrivateKeyPath: credentials.privateKeyPath, Password: credentials.password}, check)
		}
		item.Status = "passed"
		item.Message = result.Observed
		if err != nil {
			failed = true
			item.Status = "failed"
			item.Message = safePreparationError(fmt.Errorf("%w: %s", err, result.Observed))
		}
		item.ElapsedMS = time.Since(item.StartedAt).Milliseconds()
		report(item)
	}
	interpreter := "python3"
	if configured := environment.Revision.Variables["ansible_python_interpreter"]; configured != "" {
		interpreter = configured
	}
	for _, host := range inventory.Hosts {
		used := false
		for _, step := range plan.Steps {
			if step.Limit == "all" || containsNodeID(host.Groups, step.Limit) {
				used = true
				break
			}
		}
		if used {
			probe(host, "python", "prerequisite", sshcheck.Probe{Kind: "command", Target: interpreter})
		}
	}
	for _, step := range plan.Steps {
		if step.Action == domain.ActionCheck || step.Phase == "pre" || step.SourceType == "scenario_acceptance" {
			arrangement := "按锁定计划运行此 YAML"
			if step.Phase == "pre" {
				arrangement = "依赖步骤完成后、对应组件动作开始前运行此 YAML"
			}
			if step.Phase == "post" {
				arrangement = "对应组件动作完成后运行此 YAML"
			}
			if step.SourceType == "scenario_acceptance" {
				arrangement = "组件验证完成后，按验收顺序运行此 YAML"
			}
			report(PreparationCheck{ID: step.ID + ":yaml", Category: "component_check", Label: step.Name, Status: "scheduled", Source: step.Playbook, Message: "执行时检查：" + arrangement + "；实际结果记录在 Run 中"})
		}
		if step.SourceParametersFrozen {
			continue
		}
		// Use the selected source only; a registry challenge proves endpoint
		// reachability, while the media task separately verifies image identity.
		for _, media := range step.Media {
			target, kind := media.Location, "http"
			registryTransportFromDocker := false
			if media.Kind == "image" {
				reference := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
				registry, _, found := strings.Cut(reference, "/")
				if !found || !(strings.ContainsAny(registry, ".:") || registry == "localhost") {
					report(PreparationCheck{ID: step.ID + ":source:" + media.Identity, Category: "connectivity", Label: "镜像仓库访问", Status: "pending", Message: "来源没有明确仓库地址；请组件 Owner 补齐，不自动选择镜像来源"})
					failed = true
					continue
				}
				scheme := "https"
				registryTransportFromDocker = !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://")
				if strings.HasPrefix(target, "http://") {
					scheme = "http"
				}
				target, kind = scheme+"://"+registry+"/v2/", "registry"
			}
			endpoint, err := url.Parse(target)
			if err != nil || endpoint.Host == "" {
				continue
			}
			for _, host := range inventory.Hosts {
				if containsNodeID(append(append([]string{}, host.Groups...), "all"), step.Limit) {
					probe(host, step.ID+":source:"+media.Identity, "connectivity", sshcheck.Probe{Kind: kind, Target: target, Interpreter: interpreter, RegistryTransportFromDocker: registryTransportFromDocker})
				}
			}
		}
	}
	report(PreparationCheck{ID: "resources", Category: "resource_contract", Label: "资源合同与安装基线", Status: "passed", StartedAt: time.Now().UTC(), Message: "已按主机比对资源声明和已有安装清单；现场运行条件与残留由组件 YAML 在执行时检查"})
	if failed {
		return fmt.Errorf("%w: 逐主机预检存在失败项目", domain.ErrConflict)
	}
	return nil
}
