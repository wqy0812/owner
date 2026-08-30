# ClusterForge 测试环境资产目录

> 数据基线：2026-08-29 21:38（Asia/Shanghai）对 `http://192.168.88.55:8080` 的只读核对。
>
> 代码基线：2026-08-29 当前工作区。本文记录测试平台当前业务数据，不等于 `NEWPLATFORM_SEED_PROFILE=demo` 内置 Seed。
>
> 这是可复现快照，不是生产兼容矩阵。版本和环境规则见[首版与环境策略](version-policy.md)。

## 1. 当前快照

| 对象 | 数量 | 当前状态 |
| --- | ---: | --- |
| 演示身份 | 3 | 1 个 Component Owner、1 个 Scenario Owner、1 个 Environment Owner |
| Component | 15 | 全部归属 `component-alice` |
| Component Release | 15 | 全部 Released，每个 Component 1 个 Release |
| Action | 45 | 每个 Release 均为 install / verify / rollback 3/3 |
| Scenario | 1 | 当前 Revision r2，Released |
| Scenario 节点 / 边 | 21 / 50 | 所有节点选择 install |
| Environment | 2 | 均未归档；环境2是当前场景的配套六节点环境 |

当前发布目录已同步到 Git 恢复点：

- 服务端仓库：`/var/lib/clusterforge/private-catalog-repositories/k8s1175-recovery.git`
- 分支：`catalog`
- 标签：`backup/20260829T133805.356231399Z-12828`
- Commit：`158598b577d8029a1df7b651e754d57d3dbeefc3`
- 发布代次：`3/3`
- 备份时间：2026-08-29 21:38:05（Asia/Shanghai）

该恢复点是复制已发布 Component、Release、Action、Scenario Revision 和托管 Playbook 的权威入口。它不包含 Environment、Run、Approval、Notification、CredentialRef 实际值、FSS 大型介质或 Registry 镜像层。

## 2. 演示身份

| ID | 显示名 | 角色 | 负责对象 |
| --- | --- | --- | --- |
| `component-alice` | 林晓 · Runtime | Component Owner | 本文 15 个 Component |
| `scenario-carol` | 陈晨 · 集群交付 | Scenario Owner | Kubernetes 1.17.5 六节点场景 |
| `environment-dave` | 王维 · 基础设施 | Environment Owner | 两个 Kubernetes 测试环境和危险 Run 审批 |

身份切换是 Demo 会话功能，不包含密码、SSO、OIDC 或生产授权。

## 3. Component 和 Release

### 3.1 全部 Release 的共同合同

除下文显式列出的 Action 差异外，15 个 Release 具有相同的当前合同：

- 状态 `released`，Release 风险等级 `low`，`breaking=false`，`candidate=false`。
- 发布说明均为“Ubuntu 18.04 / AMD64 六节点 Kubernetes 1.17.5 细粒度交付版本。”
- 环境约束为 `architecture=amd64`、`deploymentMode=standard`、`hardwareProfile=general`、`ipFamily=IPv4`、`isolationRuntime=runc`。
- 当前 Release 没有结构化参数、Release 依赖、CredentialRef 声明、介质或镜像记录。场景的 50 条边是当前执行先后关系的唯一表达。
- 所有 Action 的 `tags=[]`、`limit=""`、`allowedParameters=[]`、`requiredCredentials=[]`、`idempotent=false`。
- verify 均为 `timeout=900`、`risk=low`、`destructive=false`；rollback 也均为 `timeout=900`，除 kubelet 和 Flannel 外均为 `risk=low`、`destructive=false`。

这些约束字段与环境2 r4 匹配。`operatingSystem`、`operatingSystemVersion` 和 `dockerVersion` 由托管 Playbook 自身的预检失败关闭，未写入当前 Release 约束。

### 3.2 组件清单

| 名称 | slug | layer | 版本 | Component ID | Release ID |
| --- | --- | --- | --- | --- | --- |
| Host Preflight | `host-preflight` | `host_foundation` | `v1.17.5-u1` | `component-44ebc1c8ece690a61d2a0ce3` | `release-e737ae0275d54923982b13bd` |
| Host Bootstrap | `host-bootstrap` | `host_foundation` | `v1.17.5-u1` | `component-0c2af6d75e931df921c2292f` | `release-ff17883d5d33bce74024f14b` |
| Cluster PKI | `cluster-pki` | `host_foundation` | `v1.17.5-u1` | `component-0bf1e3b3a674a42293bd7384` | `release-27615011c83d7e5958965bb0` |
| Kubernetes Encryption Configuration | `kubernetes-encryption-configuration` | `host_foundation` | `v1.17.5-u1` | `component-28a0079dfb70624d365df017` | `release-7333d7f02c1e94d83160f0a5` |
| Docker | `docker-runtime` | `runtime_state` | `20.10.21-u1` | `component-ec754c770406539c1af5f785` | `release-2479e87b3455bc57ee4390d2` |
| etcd | `etcd-state-store` | `runtime_state` | `3.4.3-u1` | `component-b3cbcd585773791cd5513aa2` | `release-d1dec2d93a5681366f7071df` |
| Kubernetes Distribution | `kubernetes-distribution` | `orchestration_core` | `1.17.5-u1` | `component-74a3a0326b1a88eccec22fe3` | `release-fe9e94268fcf05d5e84a9d7e` |
| kube-apiserver | `kube-apiserver` | `orchestration_core` | `1.17.5-u1` | `component-5d1f1c6c0ca4aa53beda28ef` | `release-93ca6091d6f36e3d2b181665` |
| kube-controller-manager | `kube-controller-manager` | `orchestration_core` | `1.17.5-u1` | `component-c9baa4791499da634a42aa20` | `release-0a4747b4f6dc364167187dd2` |
| kube-scheduler | `kube-scheduler` | `orchestration_core` | `1.17.5-u1` | `component-af073711204b51a89fc91acb` | `release-258ddae255f59779fa22a7ab` |
| Kubernetes Bootstrap RBAC | `kubernetes-bootstrap-rbac` | `orchestration_core` | `v1.17.5-u1` | `component-cf28e4d21137fd6a343209b2` | `release-49c5ca5bddea8b6a80956bf1` |
| kubelet | `kubelet` | `orchestration_core` | `1.17.5-u1` | `component-b3100ab911e5699246bb2537` | `release-540186c154f48453af8f7abd` |
| kube-proxy | `kube-proxy` | `orchestration_core` | `1.17.5-u1` | `component-454c6c2af03f58d81a062d35` | `release-95ff2639bc9615f72b44758b` |
| Flannel | `flannel` | `cluster_service` | `0.11.0-u1` | `component-94c0ac9e340936002a51e9e8` | `release-a63475171b00212800bf895a` |
| CoreDNS | `coredns` | `cluster_service` | `1.6.5-u1` | `component-c875271d8293911ff78d1c8a` | `release-4a5522cb1baa3c3c2b506e3e` |

用于手工复核的说明字段：

| slug | 说明 |
| --- | --- |
| `host-preflight` | Ubuntu 18.04 AMD64 六节点 CPU、内存、网络、拓扑与安装前状态检查。 |
| `host-bootstrap` | 配置 swap、内核模块、sysctl、目录和 Kubernetes 节点公共基线。 |
| `cluster-pki` | 生成并管理 Kubernetes 控制面、etcd、kubelet 与 kube-proxy 信任材料。 |
| `kubernetes-encryption-configuration` | 管理 Kubernetes API Server 静态数据加密配置。 |
| `docker-runtime` | Ubuntu 18.04 上的 Docker 20.10.21 容器运行时。 |
| `etcd-state-store` | Kubernetes 1.17.5 控制面 etcd 状态存储能力。 |
| `kubernetes-distribution` | 安装 kubeadm、kubelet、kubectl 与 Kubernetes 1.17.5 公共制品。 |
| `kube-apiserver` | Kubernetes API Server 入口与静态 Pod 能力。 |
| `kube-controller-manager` | Kubernetes Controller Manager 控制器循环能力。 |
| `kube-scheduler` | Kubernetes Scheduler 调度能力。 |
| `kubernetes-bootstrap-rbac` | 配置 kubelet TLS bootstrap 所需 token、ClusterRoleBinding 与授权。 |
| `kubelet` | Kubernetes 节点容器生命周期管理与六节点纳管。 |
| `kube-proxy` | Kubernetes Service 网络转发代理。 |
| `flannel` | Kubernetes 1.17.5 Pod 网络与 CNI 能力。 |
| `coredns` | Kubernetes 集群 DNS 服务。 |

用于手工复录的 Component 标签：

| slug | tags |
| --- | --- |
| `host-preflight` | `legacy-category:preflight`、`legacy-kind:delivery_stage`、`legacy-requiredness:core_required` |
| `host-bootstrap` | `legacy-category:bootstrap`、`legacy-kind:configuration`、`legacy-requiredness:core_required` |
| `cluster-pki` | `legacy-category:security`、`legacy-kind:artifact_set`、`legacy-requiredness:core_required` |
| `kubernetes-encryption-configuration` | `legacy-category:security`、`legacy-kind:configuration`、`legacy-requiredness:profile_required` |
| `docker-runtime` | `legacy-category:runtime`、`legacy-kind:software`、`legacy-requiredness:profile_required` |
| `etcd-state-store` | `legacy-category:state_store`、`legacy-kind:software`、`legacy-requiredness:core_required` |
| `kubernetes-distribution` | `legacy-category:control_plane`、`legacy-kind:software_bundle`、`legacy-requiredness:core_required` |
| `kube-apiserver` | `legacy-category:control_plane`、`legacy-kind:software`、`legacy-requiredness:core_required` |
| `kube-controller-manager` | `legacy-category:control_plane`、`legacy-kind:software`、`legacy-requiredness:core_required` |
| `kube-scheduler` | `legacy-category:control_plane`、`legacy-kind:software`、`legacy-requiredness:core_required` |
| `kubernetes-bootstrap-rbac` | `legacy-category:control_plane`、`legacy-kind:configuration`、`legacy-requiredness:core_required` |
| `kubelet` | `legacy-category:worker`、`legacy-kind:software`、`legacy-requiredness:core_required` |
| `kube-proxy` | `legacy-category:network`、`legacy-kind:software`、`legacy-requiredness:profile_required` |
| `flannel` | `legacy-category:network`、`legacy-kind:software`、`legacy-requiredness:profile_required` |
| `coredns` | `legacy-category:dns`、`legacy-kind:software`、`legacy-requiredness:core_required` |

### 3.3 Action 差异

| slug | install / verify / rollback hostGroup | install 超时 / 风险 | rollback 风险 | 特殊文件名 |
| --- | --- | --- | --- | --- |
| `host-preflight` | `all` | 1800 / low | low | install Action 文件名为 `host-preflight-preflight.yml` |
| `host-bootstrap` | `k8s_cluster` | 1800 / medium | low | install Action 文件名为 `host-bootstrap-configure.yml` |
| `cluster-pki` | `k8s_cert_controller` | 1800 / medium | low | 无 |
| `kubernetes-encryption-configuration` | `k8smaster` | 1800 / medium | low | 无 |
| `docker-runtime` | `k8s_cluster` | 1800 / medium | low | 无 |
| `etcd-state-store` | `k8setcd` | 1800 / medium | low | 无 |
| `kubernetes-distribution` | `k8s_cluster` | 1800 / medium | low | 无 |
| `kube-apiserver` | `k8smaster` | 1800 / high | low | 无 |
| `kube-controller-manager` | `k8smaster` | 1800 / medium | low | 无 |
| `kube-scheduler` | `k8smaster` | 1800 / medium | low | 无 |
| `kubernetes-bootstrap-rbac` | `k8smaster` | 1800 / medium | low | 无 |
| `kubelet` | `k8s_cluster` | 1800 / high | destructive，`destructive=true` | 无 |
| `kube-proxy` | `k8s_cluster` | 1800 / medium | low | 无 |
| `flannel` | `k8s_cluster` | 900 / medium | destructive，`destructive=true` | 无 |
| `coredns` | `k8smaster` | 1800 / medium | low | 无 |

普通托管路径格式为 `managed/<slug>/<release-id>/<slug>-<kind>.yml`。上表两个特殊 install 文件名依表中记录；verify 和 rollback 仍使用 `<slug>-verify.yml` 和 `<slug>-rollback.yml`。要恢复完全相同的文件内容和 SHA-256，必须使用第 6.1 节的 Git 恢复点；表格只表达业务合同，不伪造 Playbook 内容身份。

## 4. Scenario Revision

### 4.1 基本信息

| 字段 | 当前值 |
| --- | --- |
| Scenario ID | `scenario-f11afd634ac75b39847ab4f3` |
| slug | `kubernetes-1-17-5-ubuntu-fine-grained` |
| 名称 | Kubernetes 1.17.5 Ubuntu 六节点细粒度集群 |
| 说明 | 按组件分类规则编排 15 个核心 Release、21 个执行节点，面向 Ubuntu 18.04 / Docker 20.10.21 六节点测试环境。 |
| Owner | `scenario-carol` |
| 当前 Revision | r2，`scenario-revision-d3854134f1af7c5e5f4f4b23` |
| 状态 | Released |
| 完整测试通过 | 2026-08-29 21:20:18（Asia/Shanghai） |
| 发布 | 2026-08-29 21:37:35（Asia/Shanghai） |

当前 Revision 没有额外 Execution Policy。所有节点的 `values={}`、`runInputs=[]`、`dependencySources=[]`，Action 均为 install。

### 4.2 节点清单

下表 N1—N21 是本文为复现边集定义的稳定别名，不是数据库 ID。手工创建时可按坐标放置，然后按 4.3 节连线。

| 别名 | 节点标签 | Release | hostGroup | 坐标 `(x,y)` |
| --- | --- | --- | --- | --- |
| N1 | Host Preflight | `host-preflight@v1.17.5-u1` | `all` | `80,80` |
| N2 | Host Bootstrap · Control | `host-bootstrap@v1.17.5-u1` | `k8smaster` | `340,80` |
| N3 | Host Bootstrap · Worker | `host-bootstrap@v1.17.5-u1` | `k8snode` | `600,80` |
| N4 | Cluster PKI | `cluster-pki@v1.17.5-u1` | `k8s_cert_controller` | `80,250` |
| N5 | Docker · Control | `docker-runtime@20.10.21-u1` | `k8smaster` | `340,250` |
| N6 | Docker · Worker | `docker-runtime@20.10.21-u1` | `k8snode` | `600,250` |
| N7 | Distribution · Control | `kubernetes-distribution@1.17.5-u1` | `k8smaster` | `80,420` |
| N8 | Distribution · Worker | `kubernetes-distribution@1.17.5-u1` | `k8snode` | `340,420` |
| N9 | etcd | `etcd-state-store@3.4.3-u1` | `k8setcd` | `600,420` |
| N10 | Encryption Configuration | `kubernetes-encryption-configuration@v1.17.5-u1` | `k8smaster` | `80,590` |
| N11 | kube-apiserver | `kube-apiserver@1.17.5-u1` | `k8smaster` | `340,590` |
| N12 | kube-controller-manager | `kube-controller-manager@1.17.5-u1` | `k8smaster` | `600,590` |
| N13 | kube-scheduler | `kube-scheduler@1.17.5-u1` | `k8smaster` | `80,760` |
| N14 | Bootstrap RBAC | `kubernetes-bootstrap-rbac@v1.17.5-u1` | `k8smaster` | `340,760` |
| N15 | Flannel · Control | `flannel@0.11.0-u1` | `k8smaster` | `600,760` |
| N16 | Flannel · Worker | `flannel@0.11.0-u1` | `k8snode` | `80,930` |
| N17 | kubelet · Control | `kubelet@1.17.5-u1` | `k8smaster` | `340,930` |
| N18 | kubelet · Worker | `kubelet@1.17.5-u1` | `k8snode` | `600,930` |
| N19 | kube-proxy · Control | `kube-proxy@1.17.5-u1` | `k8smaster` | `80,1100` |
| N20 | kube-proxy · Worker | `kube-proxy@1.17.5-u1` | `k8snode` | `340,1100` |
| N21 | CoreDNS | `coredns@1.6.5-u1` | `k8smaster` | `600,1100` |

### 4.3 完整边集

```text
N1→N2, N1→N3,
N2→N4, N2→N5, N3→N6,
N4→N7, N4→N8, N4→N9, N4→N10, N4→N11, N4→N12, N4→N13, N4→N15, N4→N16, N4→N19, N4→N20,
N5→N15, N5→N17, N6→N16, N6→N18,
N7→N10, N7→N11, N7→N12, N7→N13, N7→N17, N7→N19,
N8→N18, N8→N20,
N9→N11, N9→N15, N9→N16,
N10→N11,
N11→N12, N11→N13, N11→N14, N11→N17, N11→N18, N11→N21,
N14→N17, N14→N18,
N15→N17, N15→N21, N16→N18, N16→N21,
N17→N19, N17→N21, N18→N20, N18→N21,
N19→N21, N20→N21
```

上述共 50 条边。导入或手工连线后，切换到“节点表”逐项核对 Release、Action、hostGroup 和前后置数，然后单击“保存草稿”和“校验”。

## 5. Environment

### 5.1 共享配置

两个环境均归属 `environment-dave`，使用：

- Variables：`IMAGE_REGISTRY=192.168.88.54:5000`、`FILE_STATION=192.168.88.57:8080`。
- CredentialRef：名称 `K8S_ENCRYPTION_KEY`，类型 `envVarRef`，引用 `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`。
- 主机 SSH 均为 `root:22`。

CredentialRef 只保存引用字符串，不包含加密密钥实际值。

### 5.2 Kubernetes 测试集群 1

- Environment ID：`environment-39570277a72a9747f0e1f289`
- 当前 Revision：r1，`environment-revision-09b9dd60fbb7a5cb16a27bc2`
- 用途：备案及后续 Kubernetes 1.34 测试；不是当前 1.17.5 场景目标。
- Facts：`architecture=amd64`、`operatingSystem=Ubuntu`、`operatingSystemVersion=18.04 / 24.04`、`ipFamily=IPv4`。

| 主机 | 地址 | 主机组 |
| --- | --- | --- |
| `master-1` | `192.168.88.78` | `k8s_cert_controller,k8setcd,k8smaster,k8s_F5` |
| `master-2` | `192.168.88.79` | `k8setcd,k8smaster` |
| `master-3` | `192.168.88.80` | `k8setcd,k8smaster` |
| `node-1` | `192.168.88.75` | `k8snode` |
| `node-2` | `192.168.88.81` | `k8snode` |
| `node-3` | `192.168.88.82` | `k8snode` |

### 5.3 Kubernetes 测试集群 2

- Environment ID：`environment-932f756b4bf4ecc3dcabecf5`
- 当前 Revision：r4，`environment-revision-21121480c9156da450b2b469`
- 用途：当前 Kubernetes 1.17.5 Ubuntu 六节点场景的配套环境。
- Facts：`architecture=amd64`、`deploymentMode=standard`、`dockerVersion=20.10.21`、`hardwareProfile=general`、`ipFamily=IPv4`、`isolationRuntime=runc`、`operatingSystem=Ubuntu`、`operatingSystemVersion=18.04`。

| 主机 | 地址 | 主机组 |
| --- | --- | --- |
| `master-4` | `192.168.88.59` | `k8s_cert_controller,k8setcd,k8smaster,k8s_F5,k8s_cluster,primary_control_plane` |
| `master-5` | `192.168.88.56` | `k8setcd,k8smaster,k8s_cluster,secondary_control_plane` |
| `master-6` | `192.168.88.58` | `k8setcd,k8smaster,k8s_cluster,secondary_control_plane` |
| `node-4` | `192.168.88.67` | `k8snode,k8s_cluster,worker_nodes` |
| `node-5` | `192.168.88.68` | `k8snode,k8s_cluster,worker_nodes` |
| `node-6` | `192.168.88.69` | `k8snode,k8s_cluster,worker_nodes` |

## 6. 复现步骤

### 6.1 精确恢复已发布目录

这是保留原 ID、原 Scenario DAG、Action Playbook 内容及 SHA-256 的唯一完整方式。只能对 Component 和 Scenario 均为空的目标库操作；不要在当前非空测试平台上提交恢复。

1. 切换到“王维 · 基础设施”，进入“灾备目录”。
2. 对空目录单击“从已有 Git 仓库恢复”，填写 `/var/lib/clusterforge/private-catalog-repositories/k8s1175-recovery.git`，单击“验证并继续恢复”。跨服务器复现时，须先把完整 bare 仓库复制到恢复目标服务端的允许根目录，再填写目标机上的实际路径。
3. 选择 `backup/20260829T133805.356231399Z-12828`，单击“预览恢复”。
4. 预览必须显示 15 个 Component、15 个 Release、1 个 Scenario 和 45 个 Playbook；同时核对 Commit 前缀 `158598b577d8029a`。
5. 输入“恢复发布目录”，单击“确认恢复空库”。
6. 恢复后读回：组件 15/15 Released、Action 45、场景 1/r2/Released、节点 21、边 50。

恢复会整批写入发布目录和 Playbook；ID/slug、用户属性、文件路径或内容身份冲突时失败且不留部分数据。

### 6.2 通过前台重新录入环境

Git Catalog 不包含 Environment。使用“环境 → 新建环境”和各配置分区按第 5 节录入：

1. 先填名称、说明、架构、操作系统、系统版本、Docker 版本和网络栈创建 r1。
2. 在 Inventory 中逐主机填写名称、地址、逗号分隔主机组、`root` 和 `22`。
3. 在“环境事实”中核对第 5 节全部 Facts；环境2不能缺少与 Release 约束对应的 6 个值。
4. 在“环境变量”中录入 `IMAGE_REGISTRY` 和 `FILE_STATION`，值只写 `host:port`。
5. 在“凭据引用”中录入 `K8S_ENCRYPTION_KEY` 的 `envVarRef`；不把真实密钥写入页面。
6. 每个分区修改都单击“保存新 Revision”，填写非空变更原因，并读回 Revision 编号、主机数、变量和脱敏引用。
7. 环境2最终应为 6 台主机、r4 结构等价快照；新建库中 Revision 编号可因保存次数不同，但当前内容必须一致。

### 6.3 通过前台手工重建业务结构

只有在不能使用第 6.1 节恢复点时才选择此路径。手工新录入会生成新 ID、新时间戳和新内容摘要，只能复现业务结构，不能伪造原历史证据。

1. 以 Component Owner 使用“批量导入”创建 15 个 Component 和 Draft；名称、slug、layer、版本、约束及 Action 按第 3 节录入。
2. 为每个 Draft 上传 install、verify、rollback 三个与目标版本一致的可审核 Playbook；不得用表格里的文件名代替真实文件内容和 SHA-256。
3. 在配套环境分别完成安装验证和回退验证，读回当前合同的发布就绪度。
4. 将所有 Draft 加入候选集。以 Scenario Owner 新建场景，按第 4.2 节加入 21 个节点，按第 4.3 节建立 50 条边。
5. 保存并校验 DAG，在环境2完成完整场景测试。
6. 使用“预览候选集并发布”，确认 15 个 Release 和 Scenario Revision 原子进入 Released。
7. 读回 15 个 Component、15 个 Released Release、45 个 Action、1 个 Released Scenario、21 个节点和 50 条边，并在“灾备目录”单击“立即备份”创建新的不可变恢复点。

## 7. 验证边界

- 本文的数量、ID、DAG、Environment Revision 和 Git 恢复点来自当前测试平台只读 API；后续业务操作可以使这个快照过时。
- 发布目录恢复可证明目录、DAG 和 Playbook 内容身份一致，不恢复环境或 Run 证据。
- Released 只表示不可变生命周期状态；真实集群当前是否安装成功，必须另外核对 Run、Environment Revision、Inventory、SSH / Ansible 连通性和目标主机状态。
- 不能把 TCP 端点可达、页面计数或本地构建成功单独当成真实 Kubernetes 收敛验收。
