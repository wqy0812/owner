# Kubernetes 1.17.5 最小组件快照

## 来源与边界

本目录来自 `paasinstallationserver/paas_installation_server/ansible/project/k8s_cluster` 的提交 `6909da3eb238b76989403f033941158ab35fdf07`，经过面向 Demo 的安全适配。源 Inventory、`group_vars`、私钥、既有 CA、静态加密配置、清理作业和安装介质均未复制。

旧的 `cert_1175.yml`、`etcd_serverless.yml`、`master_1175.yml`、`node_1175.yml` 只作为来源追溯保留，不再是平台 Release 的执行入口。平台入口统一位于 `components/`：每个逻辑组件有一个安装/配置入口和一个验证入口，共 25 组；入口只调度一个能力，不接入 recovery、clean、uninstall 或 Housekeeping。

## 组件目录

- L1：Host Preflight、Host Bootstrap、Cluster PKI、Kubernetes Encryption Configuration。
- L2：Docker `18.09.7`、etcd `3.3.10`。
- L3：Kubernetes Distribution `1.17.5`、kube-apiserver、kube-controller-manager、kube-scheduler、Bootstrap RBAC、kubelet、kube-proxy `1.17.5`。
- L4：Flannel `0.11.0`、CoreDNS `1.3.1`。
- 附加：Node Logging、HAProxy、Blackbox Exporter、Node Exporter `0.18.0`、Metrics Server `0.3.1`、AMC、GlusterFS Client `3.12.6`、Go/pprof Toolkit、Prometheus Access Bootstrap、Autoscaling RBAC。

无法从快照确认上游软件版本的 HAProxy、Blackbox Exporter、AMC 和 Go/pprof Toolkit 使用目录版本 `source-6909da3`，保持 `verified=false`。只有模板、没有任务入口的 process-exporter、Event Monitor 和 CSI 不建立可执行组件。

## 两条场景 DAG

核心 Draft 场景 `scenario-k8s-1.17.5` 从 Host Preflight 开始，分别 Bootstrap 控制节点和工作节点。PKI 进入 etcd；Docker、Distribution 和 Flannel Release 分别复用于 `k8smaster`、`k8snode`。Distribution 与 PKI 生成 Encryption Configuration，再与 etcd 汇合至 kube-apiserver；controller-manager、scheduler、Bootstrap RBAC 从 API Server 分叉。两组 kubelet 在各自 Docker、Distribution、Flannel 及 API/RBAC 完成后安装，随后安装 kube-proxy，最终汇合至 CoreDNS。

扩展 Draft 场景 `scenario-k8s-1.17.5-extended` 完整包含核心 DAG，再按适用主机组追加全部附加组件。同一 Release 在场景图中可以有多个节点；每个节点仍有独立 `hostGroup`、锁定步骤、摘要和日志。

## Fail-closed 制品合同

每个写主机的组件入口先运行 `roles/k8s1175_components/tasks/preflight.yml`。以下任一条件不满足时，在远端写操作前失败：

- `K8S_VERSION=v1.17.5`、`K8S1175_DOCKER_VERSION=18.09.7`；
- 对应核心或附加介质验真标志为 true；
- 每个外部压缩包/二进制均提供非空相对介质路径和 64 位十六进制 SHA256；
- CoreDNS、Metrics Server 等容器镜像均提供 `sha256:` digest；
- 敏感的 `K8S_ENCRYPTION_KEY` 通过 CredentialRef 动态注入，不进入 seed、SQLite 或保留日志。

环境模板只为已知介质提供路径；未知 checksum、digest 和附加介质路径保持空字符串，不提供看似可用的假默认值。HTTP 介质下载使用 `get_url checksum=sha256:...`，验证后才从远端本地文件解包。

## 保留的运行时依赖

- `/approot1/paas/admin/app/ansible/task/cert/{{ CLUSTER_ID }}` 是受控 Ansible 控制节点上的跨组件 PKI 工作区。
- `/approot1/paas/etcd`、`/approot1/paas/kube`、`/approot1/paas/exporter`、`/approot1/paas/node_amc` 和 systemd/系统配置路径沿用源 SUSE 部署布局。
- 真实执行仍需完整提供源角色引用的网络、端口、仓库、证书、集群和镜像仓库参数；本仓库不会重建或猜测被排除的 `group_vars`。

## 验证边界

`scripts/test-k8s1175-components.sh` 对 50 个入口逐一执行 `ansible-playbook --syntax-check` 和 `--list-tasks`，并断言缺失验真参数时入口 fail-closed。`make test` 和 `make build` 证明本地模型、API、前端和静态 Ansible 门禁可通过，但不证明：

- 外部介质真实存在且与声明摘要相符；
- 源角色兼容目标 SUSE 镜像和当前基础设施；
- 三控制节点加工作节点能够完成安装、服务启动和 Kubernetes 集群收敛；
- 网络、DNS、监控、存储和附加能力达到生产验收标准。

上述项目必须在可丢弃的真实目标环境中单独验收。所有写主机 Action 都是 destructive，需要环境 Owner 审批，并应先准备备份、变更窗口和回退方案。
