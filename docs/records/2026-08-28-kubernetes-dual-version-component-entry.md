# 2026-08-28 Kubernetes 双版本组件前台录入记录

> 记录类型：测试环境业务数据录入与页面验收。本文是静态历史证据，不表示这些 Draft 已可安装或发布。

## 1. 范围与初始状态

- 操作入口：`http://192.168.88.55:8080/components`。
- 操作身份：`component-alice`，页面显示“林晓 · Runtime / 组件 Owner”。
- 录入前只读计数：Component 0、Component Release 0、依赖 0、Run 0、环境安装基线 0；数据库已有 2 个 Environment 与 2 个 Environment Revision。
- 授权范围：只录组件设计数据；不创建 Action、Playbook、Artifact、Image、Run、Candidate、Released Release、Scenario 或 Environment Revision。

正式版本、分类、约束和依赖合同见[双版本组件方案](../kubernetes-dual-version-component-plan.md)。

## 2. 前台能力补齐与部署

组件页补齐了“新建空白 Draft”、多 Draft 选择、Release 风险级别、同一 Owner 私有 Draft 上游选择及前端合同校验。公开 API 和数据库结构未修改；原“克隆为新 Draft”流程保留。

每次部署均由 `scripts/deploy-test-88-55.sh` 完成测试、活动 Run 检查、数据库/二进制备份、Linux amd64 嵌入式构建、SHA-256 校验、systemd 重启、HTTP 健康检查与失败恢复：

| 北京时间 | 部署目的 | 备份目录 | 二进制 SHA-256 |
| --- | --- | --- | --- |
| 21:44 | 空白 Draft、多 Draft 选择 | `20260828T134428Z-6071` | `ddf512ddfc1d583f1f1c6eeadecad42545ecd0af81b7289cfe82122f878d27d9` |
| 21:54 | Release 风险级别入口 | `20260828T135442Z-6485` | `dcbed3f03968584e39a5724e70939f2c2d4ca5a7ebe0ef8aa3c7e6a1af97454b` |
| 22:01 | 私有 Draft 出现在上游版本下拉框 | `20260828T140101Z-6776` | `f3ba81ce1f32d2f25e3007b8e41b179b6c8fd6b2119cf8df2323bcd074756acb` |
| 22:06 | 私有 Draft 通过前端合同校验 | `20260828T140617Z-7156` | `22fd1d8bce0b6408f2ad577be1119831a06b88ed09241815774c0195603d3c42` |

最终部署服务为 `active/running`，HTTP 返回 200。所有部署均未使用 `--allow-active-runs` 或 `--rebuild-v1-db`。

## 3. 批量导入记录

21:53:09 CST，在组件中心选择“批量导入”，粘贴 17 个条目的 JSON，并由前台执行完整预检和确认。预检拓扑顺序为：

`calico → containerd → host-preflight → host-bootstrap → cluster-pki → docker → etcd → flannel → kubernetes-distribution → kubernetes-encryption-config → kube-apiserver → kube-controller-manager → kube-scheduler → kubernetes-bootstrap-rbac → kubelet → coredns → kube-proxy`

- 完整 `planDigest`：`d54d4744435e5dfed10f30556333f5d77a28f1d190a107609e0ff7ac6f7a04cc`
- 导入审计：`audit-d035de5d264d0dfba5a8bd94`
- 审计动作：`component_import.completed`
- 审计结果：17 个 Component、17 个 Draft Release；首批依赖 31；Action/Playbook 0。
- 导入边界：containerd `v2.1.1` 与 Calico `v3.32.1` 先按 1.34.3 约束创建空依赖 Draft，其余条目创建 1.17.5 Draft。

## 4. 空白 Draft 与依赖录入

21:55:58–21:58:06 CST，通过每个共享 Component 的“新建空白 Draft”录入 13 个 1.34.3 Draft。每次都显式填写版本、发布说明、风险级别和 `amd64 / Ubuntu 22.04 / IPv4 / general / runc / standard`，创建后进入对应合同编辑器。Host Preflight 为 `low`，其余为 `destructive`。

22:07:02–22:09:56 CST，按拓扑在“编辑直接依赖”中逐项选择上游 Component 和精确 Draft，填写用途并单击“保存依赖和参数”。共发生 14 次 `component_release.updated` 审计，增加 34 条 1.34.3 依赖；加上批量导入的 31 条，最终为 65 条。

## 5. 生成 ID

| Component | Component ID | 1.17.5 Release ID | 1.34.3 Release ID |
| --- | --- | --- | --- |
| Host Preflight | `component-5ccef1faa44395f02911d1a1` | `release-a53552885f88826a0af675ba` | `release-1c13c65d34ee941b66fe0bfd` |
| Host Bootstrap | `component-e4403bf3e25aec4924bcf537` | `release-b366d3d2e67122084465c26b` | `release-1d8c71aa0435dac2e8d5fc5e` |
| Cluster PKI | `component-e161248431e2b859fe964053` | `release-6f8498e983ee81bafd945a05` | `release-bbe3d845b9f734ce8e77011f` |
| Kubernetes Encryption Configuration | `component-22977672f8958cfe7b0e3174` | `release-1d35f16e175fe8174fb94f5c` | `release-30cff6467da441a570fbb705` |
| Docker | `component-07f6feb06b62eb9afe115efd` | `release-f0e317146885dd7dc61a859e` | — |
| containerd | `component-bf337ec3e63d44e846ab415c` | — | `release-802eb756f3bf2723190a65c1` |
| etcd | `component-9650805f5692639659ee5ff5` | `release-31d5fd4261fc9daf06a77c78` | `release-6f2fab3b5e3a3b498c7be80b` |
| Kubernetes Distribution | `component-aa512ffb8a0e8a16f48a11c6` | `release-4d25fe5925115afb94ff00bc` | `release-985d7390ba4e2697d651d445` |
| kube-apiserver | `component-abb7d1de6de81eba37f7a21a` | `release-8533ab4a9d3ffbb76071cf8a` | `release-f5f0a52c3b12c36c1dde81db` |
| kube-controller-manager | `component-49c1424cf04a38654257f5a3` | `release-6684d0206d959deb61643bf7` | `release-861f4ffacc992c73da4f753e` |
| kube-scheduler | `component-383d551b61a24c0a8701c65e` | `release-3e0d9e3d1991f467250a5e64` | `release-15364a2381adb4b3af0941ca` |
| Kubernetes Bootstrap RBAC | `component-7dcbc797323fa13a6aa8376e` | `release-2e82c47aecbb8b85fbace7a7` | `release-6c16208d4b3e34c98df0b6a2` |
| kubelet | `component-48c18ef6e44a5c1f863e3ce1` | `release-bad0d2be6f0d1013141ab01d` | `release-23771b78fc995d1a0e9bee9c` |
| kube-proxy | `component-f02d074935cdb489aa672877` | `release-74412c48b6cd8278f87b7a8a` | `release-849fe46f0e6f2e31ff9a4985` |
| Flannel | `component-b6a5c781a161fdf3677af932` | `release-6e3ce2d631bc40ef70f852ab` | — |
| Calico | `component-62257aee2c004e909c2c20a8` | — | `release-14e8b20e9107aa940cee653d` |
| CoreDNS | `component-7f805e7b58139ce099c2ecf5` | `release-4194d89b4301e690481e4213` | `release-f950ac56483c3a926718dec7` |

## 6. 最终验收

截至 22:12 CST，页面与数据库只读核对一致：

| 项目 | 结果 |
| --- | --- |
| Component / Draft Release / 依赖 | 17 / 30 / 65 |
| Draft / Released / Candidate | 30 / 0 / 0 |
| Release 风险 | `low` 2、`destructive` 28 |
| 非空参数 / Action | 0 / 0 |
| Artifact / Image / Image Build | 0 / 0 / 0 |
| Run / 环境安装基线 | 0 / 0 |
| Scenario | 0 |
| Environment / Environment Revision | 2 / 2，未变化 |

页面验收结果：

- CoreDNS 的 `1.6.5-u1` 与 `v1.12.2-of.1` 可在发布历史中分别选择，Readiness 随当前 Draft 切换。
- Draft 均显示生命周期、安装验证和回退验证阻断；未出现可发布误判。
- 环境 Owner 与场景 Owner 的组件目录均为 0/0，无法看到林晓的未共享私有 Draft。
- 组件 Owner 可见 17/17，并可编辑任一 Draft；浏览器控制台错误为 0。

## 7. 环境阻断（只读记录）

- `Kubernetes 测试集群 1`（`environment-3ecca40fbce8a9eb198b9afe`）r1 的页面事实为 Ubuntu `18.04 / 24.04` 混合；只读主机核对同时观察到 4.15 与 6.8 内核。它不满足单一 1.17.5 或本方案 1.34.3 Ubuntu 22.04 合同。
- `Kubernetes 测试集群 2`（`environment-176f7e121b8c4eeadd944f61`）r1 为 Ubuntu 18.04、Docker 20.10.21，只读主机核对为 kernel 4.15；不满足 1.34.3 Ubuntu 22.04 合同。
- 本次未重装主机、未创建 Environment Revision，也未把上述环境事实作为 Draft 安装、发布或兼容性证据。

## 8. 场景搭建与部署测试停止点

22:26 CST，按后续指令在前台切换为“陈晨 · 集群交付 / 场景 Owner”，进入组件中心和场景编排页核对：

- 场景 Owner 的组件目录为 `0/0`；30 个 Release 仍是组件 Owner 的私有 Draft，均未成为 Released 或就绪共享候选。
- 场景页可以打开“新建场景”，但没有可加入画布的组件版本；系统只允许场景锁定 Released 或就绪共享候选。
- 当前 Draft 均缺少 Install、Verify、Rollback 动作及安装、回退验证证据，候选与发布入口仍被 Readiness 门禁禁用。
- 两个环境也仍不满足对应 Release 合同，不能作为安装或兼容性证据。

因此未创建无组件空场景，未提交场景测试或环境运行，Scenario、Run 和环境安装基线仍保持 0。本次在可信 Action、Playbook、介质/镜像及适配环境齐备之前停止，没有绕过候选、发布或验证门禁。

## 9. 交付物盘点

在收到“仍通过前台操作并记录过程”的要求后，对仓库现有交付物做了只读盘点，结果没有改变上述停止点：

- `examples/ansible/k8s-1.17.5-cluster` 是可追溯的旧快照，但合同锁定 SUSE、Docker 18.09.7、etcd 3.3.10、CoreDNS 1.3.1，并明确要求外部介质路径、SHA-256 和镜像 digest。它与本次 Ubuntu 18.04、Docker 20.10.21、etcd 3.4.3、CoreDNS 1.6.5 Draft 不一致，不能直接复用。
- `examples/ansible/openfuyao` 包含 1.34.3 相关变量、资源和较粗粒度适配器，但来源说明明确其仍依赖私有介质、Registry、BKE 工具和适配主机；它不是本次 17 个 Component 的完整拆分生命周期交付物。
- 当前仓库没有可证明与这 30 个 Draft 一一匹配、并具备可信介质摘要或镜像 digest 的完整 Action/Playbook 集合。

因此未通过前台给新 Draft 套用旧版本 Playbook，也未填写虚假的介质摘要、镜像 digest 或环境证据。要继续到组件验证、共享候选、场景测试和环境部署，需先取得与目标版本和目标操作系统一致的交付物，并使两个测试环境满足对应 Release 合同。

## 10. 当前环境 Revision 适配版本录入

2026-08-29按后续指令继续由组件 Owner前台录入。先以场景 Owner只读查看两个环境r1：环境1事实为`amd64 / Ubuntu / 18.04 / 24.04 / IPv4`，环境2事实为`amd64 / Ubuntu 18.04 / Docker 20.10.21 / IPv4`。由于组件表单原本无法表达环境1的整体事实值`18.04 / 24.04`，先增加“18.04 / 24.04（混合）”选项及前端测试，不修改公开API或数据库结构。

部署前验证为前端102/102、前端构建及Go测试通过。守护部署结果：

- 备份：`/var/lib/clusterforge/deploy-backups/20260828T235349Z-8292`
- 二进制SHA-256：`90f39ce85ea2999d9f25f27b799b47d9f6a548ca3e928b58d972d37bc0359be8`
- 服务：`active/running`，HTTP 200

07:54:56–08:02:08 CST，切换为“林晓 · Runtime / 组件 Owner”，逐个使用“新建空白 Draft”，填写版本、发布说明、风险和环境约束；创建后在合同编辑器逐项选择同环境上游并保存。Host Preflight为`low`，其余28个新Release为`destructive`。环境1变体不填写环境中缺失的`dockerVersion`事实，Docker软件版本仍由Docker Release自身锁定。

最终兼容性复核发现两个Environment Revision也都没有`hardwareProfile`、`isolationRuntime`和`deploymentMode`事实；若Release声明这些键，PlanBuilder会因环境缺少事实而阻断。08:03–08:10 CST，仍通过前台逐个打开30个新Draft的“编辑版本与Playbook”，取消这三项并保存。最终环境1只声明`amd64 / Ubuntu / 18.04 / 24.04（混合） / IPv4`，环境2只声明`amd64 / Ubuntu 18.04 / IPv4`；按后端精确匹配规则逐Release复核，两个环境各15个Release的兼容性失败均为0。

| Component | 环境1 r1 / 1.17.5 Release ID | 环境2 r1 / 1.34.3 Release ID |
| --- | --- | --- |
| Host Preflight | `release-b317a72171e46fe4168e1503` | `release-2e9735a6af58b3dc3846ce6b` |
| Host Bootstrap | `release-a13a29eeb2648385955667cb` | `release-7f6a64921e951806cc1d8869` |
| Cluster PKI | `release-476710336fe50686bff6cf85` | `release-a5b6b7efd5928a0074d7d6dc` |
| Kubernetes Encryption Configuration | `release-7ec335367aa945843d0a83ca` | `release-ceb992777864a7a6d28f9abe` |
| Docker | `release-5be0116641e351e9ca065d65` | — |
| containerd | — | `release-a893a494468669ba12b4615f` |
| etcd | `release-2a69222614e084e46cc47816` | `release-39169fbf9c0dd52427421462` |
| Kubernetes Distribution | `release-aba5b7cf331685b4a4cc6dce` | `release-acf20e7f7c952deb053a35a9` |
| kube-apiserver | `release-09efb0ef95cb1b00077ac179` | `release-c90afe00c5302a7c8c57456f` |
| kube-controller-manager | `release-6892b158056adff3a463b74e` | `release-0d76ad142b9e3a9a4e95fffb` |
| kube-scheduler | `release-bc84254910cbb22af3de13a7` | `release-9c5decd07e5621b532075568` |
| Kubernetes Bootstrap RBAC | `release-5a9e9a01839b10bce7da9d63` | `release-09cebac175ec1c15e9f559d6` |
| kubelet | `release-6caa08915621e785df130c8a` | `release-4f1058fedec8b448476a3a97` |
| kube-proxy | `release-59fb17b271fd29262011d83d` | `release-b634ee9eddd1d066d096aadf` |
| Flannel | `release-353ff5dc310daae10337ba6a` | — |
| Calico | — | `release-e8f9e22fa14c92ed858ab998` |
| CoreDNS | `release-690906c507439e5f95565cb3` | `release-c21961af493f8808faa61704` |

最终页面和数据库只读验收：

- 环境1新建15个Draft、31条依赖；环境2新建15个Draft、34条依赖；两套依赖均精确匹配既定拓扑，缺失0、多余0、跨环境引用0。
- 总计17个Component、60个Draft Release、130条依赖；Released 0、Candidate 0。
- 参数、Action、Artifact、Image、Run、Scenario、环境安装基线仍为0；Environment和Environment Revision仍为2/2。
- 前台可选择环境1 Host Preflight `v1.17.5-u1-env1r1`并显示“18.04 / 24.04（混合）”；环境2 CoreDNS `v1.12.2-of.1-env2r1`显示并锁定Calico、kube-apiserver和kubelet三个同环境上游；浏览器控制台错误为0。

以上只完成环境事实适配版本录入，未声称软件兼容、未执行安装、未共享候选或发布。后续仍需补齐可信Action、Playbook、介质/镜像身份和组件级验证证据。

## 11. 组件 Owner 补齐预检并交环境 Owner 验证

2026-08-29 08:20–08:32 CST，继续按依赖拓扑从 Host Preflight 开始，不在先决条件尚未通过时录入或执行下游破坏性安装动作。

### 11.1 可信来源与停止边界

- 只读核对旧库和部署主机后，找到了 Kubernetes 1.17.5 的 15 组历史 Playbook及45个文件摘要；其中多个安装脚本明确断言 Ubuntu 18.04，且大量细粒度组件只记录 marker，不能证明其能在环境1的Ubuntu 18.04/24.04混合节点上完成真实安装。
- File Station仍保存`k8s-1.17.5-fine-grained-amd64.tar.gz`（SHA-256 `5fa2bb17f2d8a4bac5365dcef7f74aff69ddf739efa4d54bb86af7ad63734a08`，159044666 bytes）和`k8s-1.17.5-amd64.tar.gz`（SHA-256 `d3fab6a08a10e6d6266e5faa74eaef401eb8640520741e92d1ce315238f6a575`，350500077 bytes），但没有与本次1.34.3拆分合同匹配的完整介质和生命周期集合。
- Calico 3.32官方要求Linux kernel 5.10及以上并列出Ubuntu 20.04及以上；环境2 r1是Ubuntu 18.04、kernel 4.15。该事实不能用Release表单的字符串匹配代替技术兼容性。
- 只读主机核对还发现：环境1三个控制面当前为Kubernetes v1.34.8，不能由1.17.5组件线原地覆盖或降级；环境2三个控制面当前为Kubernetes v1.17.5，不能直接跨越多个minor升级到1.34.3。节点SSH在本机只读探测中不可达，不能据此声明全部节点状态。

因此本轮只为两套环境各补齐一个真实、只读的Host Preflight生命周期；其余28个环境适配Draft仍保持0个Action，避免在前置检查失败时伪造完整交付能力。

### 11.2 组件 Owner 前台录入

以“林晓 · Runtime / 组件 Owner”在组件中心分别选择两个环境适配Host Preflight Draft，通过“编辑版本与 Playbook”录入Install、Verify、Rollback三个动作，目标主机组均为`all`、风险均为`low`。Install与Verify复用同一只读预检Playbook；Rollback只确认预检没有产生可回退状态。

| 环境 | Release | 预检 Playbook SHA-256 | Rollback SHA-256 | 最终生命周期 |
| --- | --- | --- | --- | --- |
| 环境1 r1 / 1.17.5 | `release-b317a72171e46fe4168e1503` | `acbef959d6a47d1dcd96199dbaa9937fbcad5810e7f78852ca22f0242c05cd07` | `4ecaeb5ceca695e89ccb087429ceb566e4b00fb67247a2993db263b6e88c298c` | 3/3 |
| 环境2 r1 / 1.34.3 | `release-2e9735a6af58b3dc3846ce6b` | `3f19ed0046c0e8a2bd7a32c99c9929e7863e2d9b8169686d40c7303888e0f078` | `4ecaeb5ceca695e89ccb087429ceb566e4b00fb67247a2993db263b6e88c298c` | 3/3 |

第一次离线syntax-check发现目标运行器不接受`ansible.builtin.command`写法；未提交环境Run，立即回到前台在线编辑器改用该运行器兼容的短模块名。最终四个托管文件均通过`ansible-playbook --syntax-check`。Playbook的保存、兼容修正和Draft保存均产生`component_playbook.saved`或`component_release.updated`审计，没有通过数据库补写。

### 11.3 提交与环境 Owner 复核

私有Draft不会直接显示给环境 Owner，且在取得安装和回退证据前不能加入候选集。因此实际交接路径为：组件 Owner在前台为私有Draft预览并提交组件测试，随后切换“王维 · 基础设施 / 环境 Owner”在运行中心查看其所负责环境的Run和失败诊断。

两次计划预览都成功锁定对应Environment Revision、Install+Verify两步、Playbook摘要和备份基线，页面显示“可直接排队”。实际结果如下：

| 环境 | Run ID | 创建/完成时间 | 结果 | 环境 Owner 页面诊断 |
| --- | --- | --- | --- | --- |
| Kubernetes 测试集群 1 | `run-a4ef2a9893874a80b2b3fbd1` | 08:30:08 CST | failed，0% | `credential "K8S_ENCRYPTION_KEY" envVarRef is not configured` |
| Kubernetes 测试集群 2 | `run-87d9f20ed7cd367c5f503236` | 08:32:00 CST | failed，0% | `credential "K8S_ENCRYPTION_KEY" envVarRef is not configured` |

两个Run都在生成Ansible步骤和连接主机前失败，没有执行Host Preflight任务，也没有改变环境。环境Revision把`K8S_ENCRYPTION_KEY`标记为已配置，但平台部署进程没有对应envVarRef实际值；这是真实的第一阻断。它必须由环境凭据管理方配置真实密钥或修正Environment Revision引用，不能用占位值绕过。

截至08:33 CST只读核对为：17个Component、60个Draft Release、130条依赖、6个Action、2个failed Run、0个Released、0个Candidate、0个环境安装基线。两套Host Preflight仍因缺少成功安装和回退证据而blocked；下游28个环境适配Draft继续保持生命周期阻断。本轮未创建Scenario，未发布、未加入候选集、未执行回退，也未修改两个Environment Revision。
