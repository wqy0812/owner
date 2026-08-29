# Kubernetes 1.17.5 / 1.34.3 双版本组件方案

> 版本与环境：本文是 V1 测试环境中的组件设计合同。它不构成安装、测试、候选、发布或环境变更证据。

## 1. 目标与边界

本方案依据[组件分类规则](组件分类规则.md)，用 17 个稳定 Component 承载 Kubernetes 1.17.5 与 1.34.3 两套 Release 合同。版本差异使用 Release 表达，不复制共享逻辑组件；Docker/containerd、Flannel/Calico 是同层替代方案，不互相依赖。

本次只录入 Component、Draft Release、环境约束和精确依赖。所有 Release 保持 `parameters=[]`、`actions=[]`，不登记 Playbook、Artifact 或 Image，不执行验证 Run，不加入候选集、不发布，也不修改 Environment Revision 或安装基线。

## 2. 分类与版本矩阵

| 层 | Component | Kubernetes 1.17.5 Draft | Kubernetes 1.34.3 Draft |
| --- | --- | --- | --- |
| L1 | Host Preflight | `v1.17.5-u1` | `v1.34.3-r1` |
| L1 | Host Bootstrap | `v1.17.5-u1` | `v1.34.3-r1` |
| L1 | Cluster PKI | `v1.17.5-u1` | `v1.34.3-r1` |
| L1 | Kubernetes Encryption Configuration | `v1.17.5-u1` | `v1.34.3-r1` |
| L2 | Docker | `20.10.21-u1` | — |
| L2 | containerd | — | `v2.1.1` |
| L2 | etcd | `3.4.3-u1` | `v3.6.7-of.1` |
| L3 | Kubernetes Distribution | `1.17.5-u1` | `v1.34.3-of.1` |
| L3 | kube-apiserver | `1.17.5-u1` | `v1.34.3` |
| L3 | kube-controller-manager | `1.17.5-u1` | `v1.34.3` |
| L3 | kube-scheduler | `1.17.5-u1` | `v1.34.3` |
| L3 | Kubernetes Bootstrap RBAC | `v1.17.5-u1` | `v1.34.3-r1` |
| L3 | kubelet | `1.17.5-u1` | `v1.34.3` |
| L3 | kube-proxy | `1.17.5-u1` | `v1.34.3-of.1.icbc.harbor` |
| L4 | Flannel | `0.11.0-u1` | — |
| L4 | Calico | — | `v3.32.1` |
| L4 | CoreDNS | `1.6.5-u1` | `v1.12.2-of.1` |

网络固定为 Kubernetes 1.17.5 + Flannel 0.11.0、Kubernetes 1.34.3 + Calico 3.32.1。Calico 官方的[系统要求](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements)列出 Calico 3.32 测试 Kubernetes 1.34，[组件版本页](https://docs.tigera.io/calico/latest/reference/component-versions)确认 3.32.1 的组件版本。这里只据此确定设计版本，不把兼容矩阵当作本地安装成功证据。

## 3. Release 约束与风险

- 1.17.5：`amd64 / Ubuntu 18.04 / Docker 20.10.21 / IPv4 / general / runc / standard`。
- 1.34.3：`amd64 / Ubuntu 22.04 / IPv4 / general / runc / standard`；containerd 由自身 Release 锁定，不填写 Docker 约束。
- 两个 Host Preflight Release 的风险为 `low`；其余 28 个 Release 为 `destructive`。
- 30 个 Release 全部为私有 Draft。其他 Owner 不应看见；同一组件 Owner 可以切换任意 Draft，并可在编辑期锁定自己拥有的私有 Draft。
- Draft-to-Draft 依赖只服务于联合设计。单个 Release 发布前，Readiness 仍要求上游进入 Released 或符合共享候选合同。

## 4. 精确依赖合同

下表列出每个 Release 的直接上游。未列出的 Host Preflight 没有直接依赖；所有依赖参数映射均为空。

### 4.1 Kubernetes 1.17.5（31 条）

| 下游 | 直接上游 |
| --- | --- |
| Host Bootstrap | Host Preflight |
| Cluster PKI | Host Bootstrap |
| Docker | Host Bootstrap |
| etcd | Cluster PKI |
| Kubernetes Distribution | Cluster PKI |
| Kubernetes Encryption Configuration | Kubernetes Distribution、Cluster PKI |
| kube-apiserver | Kubernetes Distribution、Cluster PKI、Kubernetes Encryption Configuration、etcd |
| kube-controller-manager | kube-apiserver、Kubernetes Distribution、Cluster PKI |
| kube-scheduler | kube-apiserver、Kubernetes Distribution |
| Kubernetes Bootstrap RBAC | kube-apiserver |
| Flannel | Docker、etcd、Cluster PKI |
| kubelet | Docker、Kubernetes Distribution、Flannel、kube-apiserver、Kubernetes Bootstrap RBAC |
| kube-proxy | kubelet、Kubernetes Distribution、Cluster PKI |
| CoreDNS | kube-apiserver、Flannel、kubelet |

`kube-scheduler` 不重复直接锁定 Cluster PKI；它通过 kube-apiserver 与 Distribution 合同获得传递前置。该取舍使直接依赖保持 31 条，避免把所有传递前置机械展开。

### 4.2 Kubernetes 1.34.3（34 条）

| 下游 | 直接上游 |
| --- | --- |
| Host Bootstrap | Host Preflight |
| Cluster PKI | Host Bootstrap |
| containerd | Host Bootstrap |
| etcd | Cluster PKI |
| Kubernetes Distribution | Host Bootstrap |
| Kubernetes Encryption Configuration | Kubernetes Distribution、Cluster PKI |
| kube-apiserver | Kubernetes Distribution、Cluster PKI、Kubernetes Encryption Configuration、etcd、containerd |
| kube-controller-manager | kube-apiserver、Kubernetes Distribution、Cluster PKI |
| kube-scheduler | kube-apiserver、Kubernetes Distribution、Cluster PKI |
| Kubernetes Bootstrap RBAC | kube-apiserver |
| kubelet | containerd、Kubernetes Distribution、kube-apiserver、Kubernetes Bootstrap RBAC、Cluster PKI |
| Calico | kube-apiserver、kubelet、containerd |
| kube-proxy | kubelet、Kubernetes Distribution、Cluster PKI、kube-apiserver |
| CoreDNS | kube-apiserver、Calico、kubelet |

两套合同合计 65 条直接依赖。层级只用于分类；真正顺序由以上 Release 依赖及未来 Scenario DAG 共同表达。

## 5. 当前环境事实适配变体

为保留第2至4节的标准设计合同，当前测试环境使用独立的环境 Revision 变体，不直接修改原 Draft：

- 环境1 r1：1.17.5软件版本线增加 `-env1r1` Draft，约束为 `amd64 / Ubuntu / 18.04 / 24.04（混合） / IPv4`，不声明环境中不存在的 `dockerVersion`、`hardwareProfile`、`isolationRuntime`或`deploymentMode`事实。
- 环境2 r1：1.34.3软件版本线增加 `-env2r1` Draft，约束为 `amd64 / Ubuntu 18.04 / IPv4`；containerd仍由自身Release版本锁定，不把环境中额外存在的Docker事实变成1.34.3组件前置条件。
- 两套变体分别复制第4节的直接依赖拓扑，只锁定同一环境变体的上游Draft；环境1为31条，环境2为34条。

“环境事实适配”只表示PlanBuilder可对当前Environment Revision中的规范事实进行精确匹配。环境1的混合操作系统、环境2的Ubuntu 18.04/kernel 4.15是否真实支持目标Kubernetes版本，仍必须由可信Playbook、制品和真实Run证明。

## 6. 后续补齐门禁

取得可信交付物后，必须逐 Release 补齐并验证真实 Action、托管 Playbook、Artifact SHA-256、Image digest、CredentialRef 和环境证据。环境不满足 Release 约束时应先单独处理环境，不得把当前 Draft、官方兼容矩阵或历史集群结果冒充安装证据。

本次实际录入证据见[2026-08-28 双版本组件前台录入记录](records/2026-08-28-kubernetes-dual-version-component-entry.md)。
