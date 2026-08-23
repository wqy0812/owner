# 项目记忆：文档 vs 代码

基线：2026-08-18 工作区。以代码为准；文档多处滞后。

## 产品身份

- 对外文档标题：ClusterForge。仓库/二进制/Cookie/DB：NewPlatform。Go module：`codex/platform-demo`。
- 本地 Demo，不是生产控制面。4 个无密码身份：`component-alice` 林晓 · Runtime、`component-bob` 周工 · Kubernetes、`scenario-carol` 陈晨 · 集群交付、`environment-dave` 王维 · 基础设施。
- Seed 规模：34 组件、37 Release、5 场景（全部 Draft）、2 环境模板。

## 文档可信度

| 文档 | 状态 |
| --- | --- |
| `docs/backend-design.md` | 后端行为大体准确；OpenFuyao 环境变量表不全；文档日期 2026-08-13 |
| `docs/operation-manual.md` | 操作流程大体准确；§4.2 阶段选择器已不存在；§8.1 纳管 DAG 写错 |
| `README.md` | 启动/安全边界可用；只写了 1 个 OpenFuyao 场景；`make test` 描述偏窄 |
| `docs/组件分类规则.md` | **最旧**。§3–4 仍写已删除的 1.17.5 交付阶段；§4 缺大半现网组件；§5 kind 只有 3 个 |
| `docs/todo-component-public-parameters.md` | 公开参数 TODO 已全部勾完，可当已实现合同；示例 ID 与 seed 不一致 |

## 已删除，文档仍当现网

这些 ID **不存在**（`seed_test.go` 断言禁止）：

- `component-k8s-1.17.5-cert` / Etcd / Control Plane / Worker

现模型：最小逻辑组件 + 不可变 Release；同一 Release 可在 `k8smaster` 与 `k8snode` 各挂一个场景节点。

## 实际 Seed 目录

### 未进任何 DAG 的软件目录（Released + verified）

`containerd v2.1.1`、`etcd v3.6.7-of.1`、`Kubernetes v1.34.3-of.1`、`Calico v3.27.3-icbc`、`CoreDNS v1.12.2-of.1`、`kube-proxy v1.34.3-of.1.icbc.harbor`。仅作目录展示；OpenFuyao 场景只锁 `bke-*`。

### OpenFuyao（6 组件 + 3 Draft 场景 + 1 环境）

组件：`bke-cert` / `bke-bootstrap` / `bke-common` / `bke-addon`(bundle) / `bke-master`(显示名 **BKE Cluster Control Plane**，不是 Management Cluster) / `bke-nodes`。版本均 `v25.12`，`verified=false`。

| 场景 ID | DAG |
| --- | --- |
| `scenario-openfuyao` | cert → bootstrap → common → addon → master（`cluster_role=manager`） |
| `scenario-openfuyao-work-cluster` | cert → common → addon → master（`cluster_role=work`；cert 挂 `management_cluster_k8smaster`） |
| `scenario-openfuyao-work-nodes` | **仅** master verify → nodes install。手册写「verify + common + nodes」是错的 |

环境：`environment-openfuyao-template`。主机组：`bootstrap_host`、`management_cluster_k8smaster`、`work_cluster_k8smaster`、`work_cluster_k8snode`。

CredentialRef 名（手册 §8.1 对）与后端环境变量（README/设计文档配置表缺失）：

- `ansible_ssh_pass` → `NEWPLATFORM_OPENFUYAO_SSH_PASSWORD`
- `ENV_DOCKER_SECRET_USERNAME/PASSWORD` → `NEWPLATFORM_OPENFUYAO_REGISTRY_*`
- `ENV_CHART_PULL_USERNAME/PASSWORD` → `NEWPLATFORM_OPENFUYAO_CHART_*`

### Kubernetes 1.17.5（核心 15 + 扩展 10 Release，2 Draft 场景）

核心：Host Preflight / Bootstrap / Cluster PKI / Encryption Config（`v1.17.5-r1`）、Docker `18.09.7`、etcd `3.3.10`、Distribution + 三控制面进程 + kubelet + kube-proxy `1.17.5`、Bootstrap RBAC `v1.17.5-r1`、Flannel `0.11.0`、CoreDNS `1.3.1`。

扩展：Node Logging、HAProxy/`source-6909da3`、Blackbox/`source-6909da3`、Node Exporter `0.18.0`、Metrics Server `0.3.1`、AMC/`source-6909da3`、GlusterFS `3.12.6`、pprof/`source-6909da3`、Prometheus Access、Autoscaling RBAC。

场景：`scenario-k8s-1.17.5`（21 节点）、`scenario-k8s-1.17.5-extended`（39 节点）。环境：`environment-k8s-1.17.5-template`。

主机组：`k8s_cert_controller`、`k8setcd`、`k8smaster`、`k8snode`；Inventory 另有未使用的 `k8s_F5`。

加密密钥：`K8S_ENCRYPTION_KEY` → `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`。K8s Action **未**声明 `requiredCredentials`，删掉该引用不会在排队前 fail-closed。

公开参数示范：kubelet `kubeInstallRoot`（public，默认 `/approot1/paas/kube`）→ kube-proxy `kubeRoot`。依赖 ID 是 `dependency-kube-proxy-1`，节点是 `k8s1175-kubelet-master/worker`，不是 TODO 文档里的示例 ID。

## 分类合同（以 domain 为准）

- `layer`：`host_foundation` `runtime_state` `orchestration_core` `cluster_service` `observability_management` `platform_extension`
- `category`：15 个；`network` 可用于 L3 kube-proxy 或 L4 CNI
- `kind`：**5 个** — `software` `software_bundle` `delivery_stage` `configuration` `artifact_set`。分类规则 §5 漏后两个。seed 已用：Cluster PKI=`artifact_set`；Host Bootstrap / Encryption / Bootstrap RBAC / Node Logging / Prometheus Access / Autoscaling RBAC=`configuration`
- `requiredness`：`core_required` `profile_required` `optional`。BKE Nodes 是 `profile_required`，不是分类规则写的「可选」

## 环境约束三套词表（会踩坑）

| 来源 | 键 |
| --- | --- |
| 分类规则示例 | `cpuArch` / `osDistro` / `ipFamily` — **后端 matcher 不认前两个** |
| UI 写出 | `architecture` / `operatingSystem` / `ipFamily` |
| Seed facts / 手册示例 | `architecture` / `os` / `network` |
| Seed release 约束 | `architecture` / `operatingSystem` / `ipFamily` |

后端只别名：`architecture|arch`、`operatingSystem|os|distribution`、`ipFamily|network`。UI 的 `hardwareProfile`/`isolationRuntime`/`deploymentMode` 无 fact 别名，选了就会跑失败（seed 环境没这些键）。

## 前后端字段别名

API 多数双向兼容。危险点：

- Release 生命周期：后端 `status`，前端主用 `state`
- Action：后端 `kind` + `riskLevel(low|medium|high|destructive)`；前端 `type` + `risk(normal|destructive)`。组件页把已规范化的 actions 再存回去会丢掉 `name`/`riskLevel`，medium/high 变成 low
- 依赖：后端 `upstreamComponentId`/`upstreamReleaseId`；前端 `componentId`/`releaseId`
- 验证：后端只有 `verified bool`；前端还有死状态 `verification: unverified|testing|passed|failed`
- 场景 `stage`：**已删除**。手册 §4.2 仍写「界面有 Bootstrap/Management Cluster 阶段选择器」是过时的

## 实现了但文档/UI 不一致

- 5 个场景全部 Draft；文档只强调 K8s 保持 Draft
- `executionPolicy` / `maxConcurrent` 入库但不驱动调度（串行、首败即停）
- 等待审批时可 API 取消，UI 隐藏取消按钮
- 环境 Owner 可测组件/跑已发布场景；Draft 对非 Owner 不可见，所以 UI 测不了别人的 Draft
- 组件测试动作回退比文档宽：upgrade → install → configure → preflight → inspect，再追加 verify
- 无 `GET /environments/{id}`、无 users API（身份写死在 `DEMO_USERS`）、无审计页
- 迁移 `002`–`005` 存在；公开参数 TODO 勾了「不新增迁移」。`004` 才把 kind CHECK 扩到 5 个
- README 写 Node 22+ / pnpm 10+，`package.json` 无 `engines`/`packageManager`
- `make test` = `go test ./...` + vitest + Ansible 夹具 + `test-k8s1175-components.sh` + `test-openfuyao-components.sh`

## 改代码时记住

- 分层只用于展示/检索，不参与调度。顺序只看 Release 依赖 + DAG 边
- Released Release / Revision 不可改，必须克隆
- Secret 只能走 CredentialRef；参数合同禁止敏感键
- 参数优先级：默认值 → 节点值 → Run Input → 上游映射（不可本地覆盖）；环境普通参数和旧场景绑定字段已移除
- 同一环境 FIFO 单跑；Playbook 必须在 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 内
- 公开参数合同已落地，不要再引入 `parameterSchema`

## 若要修文档（未做，除非明确要求）

1. 分类规则 §3–5：删旧交付阶段，补现网组件，kind 改为 5 个，约束键改成 `architecture`/`operatingSystem`/`ipFamily`
2. 操作手册 §4.2 删阶段选择器；§8.1 纳管改为 verify+nodes；补 OpenFuyao 环境变量
3. README 写上 3 个 OpenFuyao 场景 + 2 个环境，并写全 `make test`
4. 设计文档配置表补 `NEWPLATFORM_OPENFUYAO_*`
