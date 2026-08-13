# Kubernetes 1.17.5 集群作业快照

## 来源与范围

本目录是面向 demo 的安全适配快照，来源为：

- 仓库：`paasinstallationserver/paas_installation_server/ansible/project/k8s_cluster`
- 源提交：`6909da3eb238b76989403f033941158ab35fdf07`
- 四个入口：`cert_1175.yml`、`etcd_serverless.yml`、`master_1175.yml`、`node_1175.yml`
- 四个辅助任务：`varutil.yml`、`vm_check.yml`、`vm_check2.yml`、`f5_check.yml`
- 四个角色：`k8s1175_cert`、`k8setcd_serverless`、`k8s1175master`、`k8s1175node`

这不是完整生产交付包。源目录中的 `hosts`、全部 `group_vars` 和清理作业均未复制；本目录也没有 1.17.5 安装介质。

## 安全适配

快照有意不保持逐字节一致，包含以下 fail-closed 改动：

1. 未复制源证书角色中的 `ca-key.pem`、`ca.pem`、`cb-ca.pem`。`k8s1175_cert` 改为使用 `ca-csr.json` 和 cfssl，按 `CLUSTER_ID` 在运行时生成独立 CA，将生成的 `ca.pem` 复制为 `cb-ca.pem`，并把 CA 私钥权限限制为仅所有者可读写；所有生成私钥的 cfssl 任务均使用 `no_log`。
2. 未复制 master 角色中的静态 `encryption-config.yaml` 和 `jupyterhub.yaml`。`encryption-config.yaml.j2` 从 Ansible extra-var `K8S_ENCRYPTION_KEY` 注入密钥；平台运行时应通过 CredentialRef 提供该变量，不把真实值写入仓库。相关断言和模板任务使用 `no_log`。
3. 新增标记为 `always` 的 `preflight_1175.yml`，四个入口在角色执行前都会检查：`k8s_cert_controller` 恰好 1 台、`k8setcd` 恰好 3 台、`k8smaster` 恰好 3 台、`k8snode` 至少 1 台、`k8s_F5` 至少 1 台，`K8S_VERSION` 必须为 `v1.17.5`，介质必须已人工验真，且加密密钥必须是 32 字节数据的标准 Base64 表示。预检委派到唯一的 `k8s_cert_controller`，不能通过 Action 的 tags 绕过。
4. 证书入口从隐式 `127.0.0.1` 改为显式本地组 `k8s_cert_controller`，便于平台按 inventory 和 limit 调度。该组应只包含受控的 Ansible 控制节点。
5. 源 master/node 角色虽然标为 1.17.5，下载变量却仍叫 `K8SMASTER_1173_CERT` 和 `K8SNODE_1173_CERT`，无法从名字或仓库内容证明介质版本。快照改用 `K8SMASTER_1175_CERT` 和 `K8SNODE_1175_CERT`；两者的路径必须包含 `1.17.5`，且只有在独立校验制品来源、签名或校验和后才能设置 `K8S1175_ARTIFACTS_VERIFIED=true`。此布尔值只是人工验真的门禁声明，不会代替校验和验证。
6. 为兼容现代 Ansible，任务级旧式 `- include:` 已机械替换为 `- import_tasks:`；动态变量加载 `include_vars` 保持不变。
7. master 角色创建 Prometheus kubeconfig 时会读取 Kubernetes Secret 名称和 token；读取、保存、模板渲染及初始化链路均标记 `no_log`，避免失败输出或高详细度日志暴露动态凭据。
8. `vm_check.yml`、`vm_check2.yml` 和 `f5_check.yml` 原本从源仓库的固定 `/approot1/.../project/k8s_cluster/roles/...` 路径读取控制端资源，复制到 demo 后无法运行。快照已改为从 `{{ playbook_dir }}/roles/k8s1175node/...` 读取；远端脚本仍写入 `/tmp`。`varutil.yml` 没有内置资源路径，继续按部署方传入的 `VAR_FILES` 加载控制端变量文件。
9. `varutil.yml` 原有的变量文件路径调试输出已删除。受控变量文件可能包含内部路径或其他配置元数据，不应在保留日志中展开。
10. 平台的四个 Install Action 固定使用 `--tags install`。快照已从入口断开源角色中的 `update_docker` 和 4243 iptables 分支，并删除安装期间清空 journal 的任务；仅标记为 `recovery` 的任务也不会执行。如需恢复或 Docker 卸载重装，应另行建模并单独评审，不能通过本场景触发。
11. Docker 配置只保留本地 Unix socket，移除了无 TLS 的 `0.0.0.0:4243` Remote API 及对应 AMC 端口检查。快照不再删除 zypper 软件源，也不再从 HTTP/GPG-disabled 仓库安装 Docker；环境 Owner 必须预置并验真精确版本，通过 `K8S1175_DOCKER_RUNTIME_VERIFIED` 与 `K8S1175_DOCKER_VERSION` fail-closed 校验。controller/master/etcd 私钥均只允许对应所有者读取；Prometheus token 临时脚本限制为仅所有者可读写执行，并通过 `always` 清理。
12. 默认 master 入口不再部署历史 Housekeeping DaemonSet。该镜像要求把 API Server 私钥注入容器，同时还挂载宿主机证书树；即使从 ConfigMap 改成 Secret 也不能消除私钥分发和容器逃逸后的影响。若业务确需该能力，应改为独立组件，使用专用 ServiceAccount、最小 RBAC 与独立客户端凭据后再评审。
13. 快照不再把通用 `k8s` 证书组织绑定为 `cluster-admin`，也不会向 worker 分发 `system:masters` 的 admin kubeconfig。聚合代理改用独立 front-proxy CA，并将 `requestheader-allowed-names` 固定为 `front-proxy-client`；etcd 本地 client endpoint 同样只保留 HTTPS/mTLS，关闭 pprof。
14. 默认入口断开了 blackbox exporter、AMC、GlusterFS、Go/pprof 等非核心附加组件，避免未绑定 checksum 的次级 HTTP/RPM 介质混入基础集群搭建；这些能力如需启用，应作为独立组件固定制品摘要后再接入。master/node 在任何远端写操作前会读取实际 Docker Server 版本并与独立验真声明比对。

## 保留的绝对路径依赖

可运行性审计区分了控制端资源路径、跨阶段状态和目标机运行时布局。以下依赖不是遗漏的 demo 源文件，不能机械改成 `{{ playbook_dir }}`：

- `/approot1/paas/admin/app/ansible/task/cert/{{ CLUSTER_ID }}` 是唯一 `k8s_cert_controller` 上的持久化跨阶段工作区。证书阶段在此生成 CA、组件证书和 kubeconfig；etcd/master/node 阶段的 `copy` 从此控制端目录分发证书；master/node 的 `fetch` 也写回此目录。四步必须使用同一控制节点、同一 `CLUSTER_ID`，且该目录需预先可写并受限保护。
- `/approot1/paas/admin/app/ansible/task/cert/cfssl` 和 `cfssljson` 是证书阶段从 `CFSSL_MEDPATH` 解包到上述控制端工作区的可执行文件；介质内容、Linux 架构兼容性和执行权限必须在运行前确认。
- 三个远端角色会在控制端读取 `~/.ssh/ansible_key.pub`，再写入目标机 `sysop` 的 `authorized_keys`。运行用户必须显式提供并批准该公钥；这是授权变更，不能把任意 demo 公钥内置到仓库。
- `VAR_FILES` 中的每个路径都由 `include_vars` 在控制端解析。平台必须传入控制端可访问的受控变量文件；真实 `group_vars` 仍不进入本快照。
- `/approot1/paas/etcd`、`/approot1/paas/kube`、`/approot1/paas/exporter`、`/approot1/paas/node_amc` 以及 `/usr/lib/systemd/system`、`/etc`、`/var`、`/root`、`/tmp` 等是源角色约定的目标机安装和系统路径。它们涉及目录、服务、网络与日志变更，只有目标系统布局和权限满足源约定时才可运行。
- `IHS_IP`/`IHS_PORT` 组成的 HTTP 地址及 `CFSSL_MEDPATH`、`ETCD_MEDPATH_SERVERLESS`、`K8SMASTER_1175_CERT`、`K8SNODE_1175_CERT` 等变量指向外部介质站，不是仓库内资源；下载可达性、校验和和 1.17.5 版本归属仍需独立验证。

## 执行前阻断项

真实 `hosts` 和 `group_vars` 被刻意排除，因此必须由部署方通过平台环境参数、CredentialRef、受控 inventory 和受控变量文件补齐角色引用的动态变量。除预检变量外，还包括 `CLUSTER_ID`、`ANSIBLE_USER`、`IHS_IP`、`IHS_PORT`、端口、网络、镜像/介质路径、`VAR_FILES`、`ENABLE_VM_CHECK` 等源环境参数。缺任一实际执行路径所需变量都应视为阻断，不能以语法检查通过替代。

至少需要提供：

- inventory 组：`k8s_cert_controller`、`k8setcd`（3）、`k8smaster`（3）、`k8snode`（至少 1）、`k8s_F5`（至少 1）；
- `K8S_VERSION=v1.17.5`；
- 目标 master/node 已预置 Docker，并在独立验真后设置 `K8S1175_DOCKER_RUNTIME_VERIFIED=true` 与精确的 `K8S1175_DOCKER_VERSION`；
- 已独立验真的 `K8SMASTER_1175_CERT` 和 `K8SNODE_1175_CERT`，路径包含 `1.17.5`；
- 校验完成后才设置 `K8S1175_ARTIFACTS_VERIFIED=true`；
- 通过 CredentialRef/Ansible Vault 等受控通道传入 `K8S_ENCRYPTION_KEY`，值为随机 32 字节密钥的标准 Base64（44 个字符），不要放入普通变量文件、日志或命令历史。

## 建议顺序

这是三个阶段、四个步骤的作业：

1. 证书阶段：`cert_1175.yml`；
2. 控制面阶段：先 `etcd_serverless.yml`，再 `master_1175.yml`；
3. 工作节点阶段：`node_1175.yml`。

etcd 和 master 拓扑均按三个成员编写；模板直接索引三个成员，不能缩减成单节点或双节点后直接运行。

## 破坏性说明

这些角色会在目标机创建或删除用户/文件，修改 systemd、内核、网络、iptables、日志、容器运行时和 Kubernetes 配置，并启停或重启服务。它们不是只读 demo，也不包含自动回滚。只有在资产所有者批准、变量和介质复核、备份/回滚方案就绪、变更窗口明确且目标机器确认无误后才可执行；优先在可丢弃环境验证。

`ansible-playbook --syntax-check` 只证明本地 YAML、角色和静态导入可解析，不证明介质可下载、远端系统兼容、三节点收敛或集群可用。
