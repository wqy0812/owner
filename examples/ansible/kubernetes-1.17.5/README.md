# Kubernetes 1.17.5 当前组件参考

本目录依据 2026-09-08 从 88.55 测试平台读取的 **15 个已发布组件 Release、1 个已发布 Scenario Revision** 整理，作为当前原生 Role 编写参考。源场景为「Kubernetes 1.17.5 测试环境 2」r1。精确来源、原文件 SHA-256、重构后的 SHA-256 和变换记录见 [source-lock.json](source-lock.json)。源发布记录的验收证据只属于原合同，不自动适用于本目录。

运行目标：Ubuntu 18.04 / amd64 / IPv4，Ansible **2.8.8**、Python **3.6.9**，三个控制节点、三个工作节点。组件 Python 文件逐字保留来源实现；每个工作区独立携带运行脚本，无跨组件文件依赖。

## 文件与动作边界

| 路径 | 用途 |
| --- | --- |
| `roles/<组件>/contract.json` | 动作、参数提供方、精确依赖版本、参数映射、镜像摘要和介质清单的参考说明 |
| `roles/<组件>/tasks/precheck.yml` | 安装前检查，绑定到 install 的前置检查 |
| `roles/<组件>/tasks/install.yml` | 保存或核验原始恢复基线，再安装与配置 |
| `roles/<组件>/tasks/postcheck.yml` | 安装后检查，绑定到 install 的后置检查 |
| `roles/<组件>/tasks/rollback.yml` | 校验原基线和恢复前条件，撤销本组件资源并恢复原状态 |
| `roles/<组件>/tasks/rollback-postcheck.yml` | 恢复后检查，绑定到 rollback 的后置检查 |
| `roles/<组件>/files/` | 此组件自己的脚本；Cluster PKI 另有证书分发脚本 |
| [scenario.json](scenario.json) | 节点、主机组、30 条执行依赖、4 条手工顺序边，以及正序安装、逆序回退清单 |
| `acceptance/tasks/acceptance.yml`、`acceptance/files/acceptance.py` | 场景业务验收工作区 |
| [inventory.example.yml](inventory.example.yml) | 三控制节点、三工作节点的分组示意，使用文档地址 |

`contract.json` 和 `scenario.json` 是供 Owner 录入和审核的参考清单，**不是平台 API 导入格式，也不是可直接执行的 Playbook**。动作文件是原生 task list，没有 `hosts:` 或跨组件编排；平台负责执行主机范围、步骤顺序、凭证和恢复身份。没有默认 `main.yml`，以免把安装、检查和回退误连成一个动作。

## 场景与组件职责

下表按已发布场景的确定顺序排列。安装在每个节点执行安装前检查、安装、安装后检查，再推进下一节点；全场景回退按表中逆序执行各组件 rollback 和恢复后检查。配置依赖的参数映射保留在组件合同中，不当作新的执行顺序边。

| 顺序 | 组件 | 目标主机 | 职责 |
| --- | --- | --- | --- |
| 1 | host-preflight | 全部六节点 | OS、架构、swap、空间、占用端口及既有集群检查 |
| 2 | host-bootstrap | 全部六节点 | 内核模块、sysctl、网络规则及原始状态恢复 |
| 3 | docker-runtime | 全部六节点 | 校验已安装 Docker 20.10.21 与数据目录，管理服务状态 |
| 4 | kubernetes-distribution | 全部六节点 | 校验已安装 Kubernetes 1.17.5 二进制，按摘要安装 CNI 0.8.5 |
| 5 | cluster-pki | 全部六节点 | 首控制节点签发，按主机分发证书及 kubeconfig |
| 6 | kubernetes-encryption-configuration | 三控制节点 | 通过 CredentialRef 配置 Secret 加密 |
| 7 | kubelet | 全部六节点 | kubelet 配置及静态 Pod 工作目录 |
| 8 | etcd-state-store | 三控制节点 | etcd 3.4.3 静态 Pod、数据与仲裁检查 |
| 9 | kube-apiserver | 三控制节点 | API 服务、证书与加密配置 |
| 10 | kube-controller-manager | 三控制节点 | 控制管理器 |
| 11 | kube-scheduler | 三控制节点 | 调度器 |
| 12 | kubernetes-bootstrap-rbac | 三控制节点 | 首控制节点写入 RBAC，三个控制节点分别检查 |
| 13 | kube-proxy | 全部六节点 | 首控制节点管理 DaemonSet，检查节点网络规则 |
| 14 | flannel | 全部六节点 | Flannel 0.22.3、版本绑定的 CNI 插件及网络资源 |
| 15 | coredns | 三控制节点 | CoreDNS 1.6.5、DNS Service 与资源归属 |

原始 Release 版本号（包括 `env2.1` 后缀）仅作为来源版本锁定；制作新的 Release 时由 Owner 指定新版本，并重新锁定本次发布的精确上游 Release。

## 复用到平台

1. 组件 Owner 按合同新建 Draft，逐个录入参数、依赖和五个动作；上传该组件 `files/` 下的文件，将对应 `tasks/` 内容放到平台生成的动作入口，关联前后检查。不要使用参考动作 ID 代替平台生成的 ID。
2. 环境 Owner 绑定本环境的控制节点与工作节点。本参考用 `control_plane`、`worker_nodes` 表达角色；当前平台的真实组名可能是 `group_…`，需要在 Draft 任务中的 `groups[...]`、PKI 委派目标及场景主机组中统一替换。按 inventory 顺序选择首控制节点，安装与回退必须保持相同身份。每个入口都会先检查恰好六个不重复主机、3+3 分组以及六个不同的 `ansible_host`。
3. `cf.inputs` 由平台根据参数合同、依赖映射和介质解析生成。镜像通过 `<logicalName>_image_ref` / `<logicalName>_image_digest` 输入；CNI 介质使用 `cni_plugins_url` / `cni_plugins_sha256`。清单中的 `*.example.invalid` 必须换成 Owner 管理的实际来源，保留已核验的摘要；不要将来源 URL 或镜像仓库写回动作脚本。
4. `clusterforge_backup_ref`、`clusterforge_backup_marker`、`clusterforge_backup_metadata` 属于平台生成的恢复身份。回退必须沿用原安装的基线，不手工伪造或换成新运行的空目录。证书与加密配置均进入受保护的原始基线。
5. 加密组件的 `K8S_ENCRYPTION_KEY` 通过动作声明的 CredentialRef 注入，需为 32 字节随机密钥的 Base64 编码；不放入参数默认值、清单或普通日志。PKI 分发和引用该凭证的脚本保留 `no_log: true`。
6. 场景 Owner 按 `scenario.json` 绑定新组件 Release，保留配置映射和四条手工顺序边，录入验收工作区及 `admin_kubeconfig_path` 的 Cluster PKI 上游绑定。工作区或组名改变后，重新完成组件安装/回退测试、审核、发布及场景测试，不能复用源环境的测试状态。

## 来源能力与限制

这是在准备好的六节点基础环境上交付集群的参考，不包含操作系统安装或 Docker/Kubernetes 软件包供应。安装前要求 Docker 20.10.21 已运行、使用 cgroupfs，`/usr/bin/{kubeadm,kubelet,kubectl}` 为 1.17.5，swap 关闭，所需工具可用且没有正在运行的其他容器或既有集群。

来源实现存在固定路径和网络布局假设：PKI 位于 `/etc/kubernetes/pki`，kubeconfig 位于 `/etc/kubernetes`，证书包含默认 Service IP `10.96.0.1`。参考组合使用 Service CIDR `10.96.0.0/12`、Pod CIDR `10.244.0.0/16` 和 DNS IP `10.96.0.10`。`control_plane_endpoint` 必须由环境 Owner 提供并与证书覆盖的控制节点地址匹配；任意新 VIP、域名、路径或网段不因参数存在便获得支持，需同步调整实现和验证。

恢复脚本保留文件内容、权限、uid/gid、符号链接、服务及相关网络状态；拒绝身份不符的基线、未归属的现有 API 对象，以及 UID 已替换的对象。防火墙出现无关规则漂移时停止恢复，避免覆盖外部改动。这些机制不等同于所有并发运维场景均已验证。

## 业务验收

验收在三个控制节点分别运行，`mayMutate: true`，检查六节点 Ready、三个 API、etcd 仲裁、控制面和插件、36 对 Pod HTTP 通信、Service/DNS，以及 Secret API 回读和 etcd 内的 aescbc 密文。

探针从已交付的 Flannel DaemonSet 读取镜像引用，核验与来源固定摘要一致后检查工具并创建探针。仓库地址变化不需要改验收脚本，摘要变化需要新的参考与验证。验收成功后仅清理本次 UID 和 Owner 一致的命名空间；失败时保留探针及定位信息供排查，不自动删除。

## 本地验证

在固定 `clusterforge-test-ubuntu:18.04-ansible2.8.8` 容器的源码副本中运行：

```sh
make test-reference-playbooks ANSIBLE_PLAYBOOK=/opt/ansible/bin/ansible-playbook
```

门禁用产品的 Role task 验证器检查全部 76 个入口，以 Ansible 2.8.8 进行语法及任务展开检查，并在临时目录验证参数/顺序映射、来源摘要、回退基线、API 资源归属、防火墙漂移、分组失败关闭和探针镜像选择。测试中的命令替身不访问真实集群，不能代替新工作区的六节点安装、业务验收和回退实测。

旧 `k8s-1.17.5-cluster`、`k8s-1.17.5-kubeadm`、`openfuyao` 继续作为历史源码和夹具保留，其原门禁改名为 `make test-historical-reference-playbooks ANSIBLE_PLAYBOOK=...`。旧快照在 2.8.8 上的已知兼容失败仍保留记录；当前门禁通过不表示旧快照已修复。
