# ClusterForge Demo 资产目录

> 文档基线：2026-08-28 当前工作区 Seed 代码
> 版本与环境：本文只描述 `NEWPLATFORM_SEED_PROFILE=demo` 创建的 V1 测试数据，不代表生产目录。统一规则见[首版与环境策略](version-policy.md)。

## 1. 总览

完整 Demo Seed 当前创建：

| 资产 | 数量 | 状态说明 |
| --- | ---: | --- |
| 演示身份 | 4 | 无密码切换，仅用于本地 Demo |
| Component | 34 | 分属两个 Component Owner |
| Component Release | 37 | 目录 Release 与作业 Release 状态不同，见下文 |
| Scenario | 5 | 当前全部为 Draft |
| Environment | 2 | 均为 TEST-NET 或模板配置 |

`NEWPLATFORM_SEED_PROFILE=demo` 创建上表中的 4 个身份；`NEWPLATFORM_SEED_PROFILE=identities` 只创建 `component-alice`、`scenario-carol`、`environment-dave` 3 个操作身份，不创建组件、场景或环境。

## 2. 演示身份

| ID | 显示名 | 角色 |
| --- | --- | --- |
| `component-alice` | 林晓 · Runtime | Component Owner |
| `component-bob` | 周工 · Kubernetes | Component Owner |
| `scenario-carol` | 陈晨 · 集群交付 | Scenario Owner |
| `environment-dave` | 王维 · 基础设施 | Environment Owner |

身份切换不包含密码认证、外部 IAM 或生产权限能力。

## 3. 独立软件目录

以下 6 个 Release 为 Released，用于目录和依赖选择；它们不进入当前 OpenFuyao 场景：

| 组件 | Release |
| --- | --- |
| containerd | `v2.1.1` |
| etcd | `v3.6.7-of.1` |
| Kubernetes | `v1.34.3-of.1` |
| Calico | `v3.27.3-icbc` |
| CoreDNS | `v1.12.2-of.1` |
| kube-proxy | `v1.34.3-of.1.icbc.harbor` |

Released 是不可变生命周期状态，不等于本次工作区对真实集群重新执行后的验收结论。详情页 Readiness 只根据当前合同和数据库中的安装/回滚双证据派生。

## 4. OpenFuyao 样例

### 4.1 组件和 Release

OpenFuyao 包含 6 个组件：`bke-cert`、`bke-bootstrap`、`bke-common`、`bke-addon`、`bke-master` 和 `bke-nodes`。版本均为 `v25.12`，当前 Release 为 Released；Seed 不伪造真实环境的安装/回滚双证据。

`bke-addon` 是否聚合多个动作或制品由 Release 实际内容表达；`bke-master` 显示为 **BKE Cluster Control Plane**，`bke-nodes` 显示为 **BKE Work Nodes**。

### 4.2 三个独立 Draft 场景

| 场景 ID | 锁定 DAG |
| --- | --- |
| `scenario-openfuyao` | cert → bootstrap → common → addon → master，`cluster_role=manager` |
| `scenario-openfuyao-work-cluster` | cert → common → addon → master，`cluster_role=work` |
| `scenario-openfuyao-work-nodes` | master verify → nodes install |

业务节点纳管场景只先验证已有业务控制面，再执行节点纳管；不会重复执行 common 或 addon。

### 4.3 环境模板和凭据

环境 `environment-openfuyao-template` 使用主机组：

- `bootstrap_host`
- `management_cluster_k8smaster`
- `work_cluster_k8smaster`
- `work_cluster_k8snode`

模板使用 TEST-NET 地址，并声明以下 CredentialRef：

| CredentialRef | 后端进程环境变量 |
| --- | --- |
| `ansible_ssh_pass` | `NEWPLATFORM_OPENFUYAO_SSH_PASSWORD` |
| `ENV_DOCKER_SECRET_USERNAME` | `NEWPLATFORM_OPENFUYAO_REGISTRY_USERNAME` |
| `ENV_DOCKER_SECRET_PASSWORD` | `NEWPLATFORM_OPENFUYAO_REGISTRY_PASSWORD` |
| `ENV_CHART_PULL_USERNAME` | `NEWPLATFORM_OPENFUYAO_CHART_USERNAME` |
| `ENV_CHART_PULL_PASSWORD` | `NEWPLATFORM_OPENFUYAO_CHART_PASSWORD` |

这些变量没有默认值，部署方必须在执行前按环境实际情况配置；实际值不会进入 Seed、Run 快照或前台响应。

## 5. Kubernetes 1.17.5 样例

### 5.1 组件与 Release

Kubernetes 1.17.5 样例包含 15 个核心 Release 和 10 个扩展 Release。Release 均为 Released；它们来自作业快照和静态门禁，Seed 不写伪造证据，因此不代表真实六节点安装已通过。

核心能力包括 Host Preflight、Host Bootstrap、Cluster PKI、Encryption Configuration、Docker、etcd、Distribution、三个控制面进程、Bootstrap RBAC、Flannel、kubelet、kube-proxy 和 CoreDNS。

扩展能力包括 Node Logging、HAProxy、Blackbox Exporter、Node Exporter、Metrics Server、AMC、GlusterFS Client、Go/pprof Toolkit、Prometheus Access 和 Autoscaling RBAC。无法从来源快照确认软件版本的 Release 使用 `source-6909da3`，不推测上游版本。

以下旧聚合组件 ID 不存在：

- `component-k8s-1.17.5-cert`
- `component-k8s-1.17.5-etcd`
- `component-k8s-1.17.5-master`
- `component-k8s-1.17.5-node`

当前使用最小逻辑组件和不可变 Release；同一 kubelet、kube-proxy、Docker、Distribution 或 Flannel Release 可以分别出现在控制节点和工作节点上，节点 `hostGroup` 决定目标范围。

### 5.2 场景与环境

| 场景 | 节点数 | 说明 |
| --- | ---: | --- |
| `scenario-k8s-1.17.5` | 21 | 核心安装 DAG |
| `scenario-k8s-1.17.5-extended` | 39 | 完整包含核心 DAG，并追加扩展能力 |

两个场景当前均为 Draft。环境模板为 `environment-k8s-1.17.5-template`，主机组包括 `k8s_cert_controller`、`k8setcd`、`k8smaster` 和 `k8snode`；Inventory 还保留未被当前场景节点使用的 `k8s_F5` 组。

### 5.3 参数映射与加密密钥边界

kubelet 的公开参数 `kubeInstallRoot` 默认值为 `/approot1/paas/kube`；kube-proxy 通过依赖 `dependency-kube-proxy-1` 把它映射到本地参数 `kubeRoot`。控制面和工作节点各自显式选择对应的 kubelet 来源节点。

环境模板声明 `K8S_ENCRYPTION_KEY` → `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`。当前 Kubernetes 1.17.5 Action 尚未把 `K8S_ENCRYPTION_KEY` 列入 `requiredCredentials`，因此缺少该引用不会在创建 Run 前由通用凭据校验阻断；实际 Playbook 的预检仍必须自行失败关闭。在补齐 Action 合同前，环境 Owner 应在审批前人工确认引用和后端环境变量均已配置。

## 6. 验证边界

- Seed 数量、DAG 结构、参数映射和合同由 Go 测试覆盖。
- `make test` 的 Ansible 脚本门禁验证语法、入口和静态合同；本机缺少 `ansible-playbook` 时必须报告为未验证。
- 本地测试或构建不能替代 Registry 推送、文件站传输、目标主机预检、真实安装、验证和回退演练。
- Demo 环境地址、凭据引用和组件状态可能随 Seed 代码变化；修改 `internal/seed` 时必须同步更新本文。
