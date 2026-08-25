# 测试环境节点快照（2026-08-20，更新于 2026-08-24）

> 版本与环境：本文记录项目首个版本（V1）的测试环境节点，不是生产环境清单，也不能作为生产部署或生产验收依据。统一规则见 [首版与环境策略](version-policy.md)。

> 证据边界：2026-08-24 已逐台通过 SSH 核对主机名，并通过 ClusterForge 前台分别完成两套环境的 TCP 连通性检查。连通性检查只覆盖 12 台主机的 SSH 端口、`IMAGE_REGISTRY` 和 `FILE_STATION`，不等于 SSH 鉴权、Ansible 执行、Kubernetes 安装或真实业务验收。部署或执行破坏性作业前仍须重新确认 Inventory、端点、凭据和活动 Run。

> 历史属性：下列“r3”和端点状态是 2026-08-24 初次检查时的观察值，不是实时状态。同日后续细粒度交付验收已把测试集群 2 的环境事实规范化并保存为 r6，详见 [Kubernetes 1.17.5 Ubuntu 细粒度场景部署验收记录](records/2026-08-24-k8s-1.17.5-ubuntu-fine-grained-acceptance.md)。

## 基础服务节点

| IP | MAC | 节点名 | 用途 | 当日记录入口 |
| --- | --- | --- | --- | --- |
| `192.168.88.54` | `00:15:5D:58:8F:07` | deploy | Docker 镜像仓库节点 | `192.168.88.54:5000` |
| `192.168.88.55` | `00:15:5D:58:8F:06` | ansible | ClusterForge 平台及 Ansible 执行节点 | `http://192.168.88.55:8080/` |
| `192.168.88.57` | `00:15:5D:58:8F:08` | fss | File Station 组件介质服务器 | `192.168.88.57:8080` |

`192.168.88.54` 和 `192.168.88.57` 是两套集群共享的外部端点，不属于任一 Kubernetes Inventory。环境变量必须填写 `host:port`，不能带 `http://`、`https://`、镜像 tag 或 digest：

```text
IMAGE_REGISTRY=192.168.88.54:5000
FILE_STATION=192.168.88.57:8080
```

## Kubernetes 测试集群 1

ClusterForge 环境名称：`Kubernetes 测试集群 1`。2026-08-24 初次前台检查时观察到 Revision `r3`，架构为 `amd64`，操作系统记录为 `Ubuntu 18.04 / 24.04`，网络栈为 `IPv4`。

| IP | MAC | 节点名 | Inventory 分组 | 用途 |
| --- | --- | --- | --- | --- |
| `192.168.88.78` | `00:15:5D:58:8F:00` | master-1 | `k8s_cert_controller`, `k8setcd`, `k8smaster`, `k8s_F5` | 证书控制节点、etcd、Kubernetes 控制节点、API 入口 |
| `192.168.88.79` | `00:15:5D:58:8F:01` | master-2 | `k8setcd`, `k8smaster` | etcd、Kubernetes 控制节点 |
| `192.168.88.80` | `00:15:5D:58:8F:02` | master-3 | `k8setcd`, `k8smaster` | etcd、Kubernetes 控制节点 |
| `192.168.88.75` | `00:15:5D:58:26:06` | node-1 | `k8snode` | Kubernetes 工作节点 |
| `192.168.88.81` | `00:15:5D:58:26:0B` | node-2 | `k8snode` | Kubernetes 工作节点 |
| `192.168.88.82` | `00:15:5D:58:26:0C` | node-3 | `k8snode` | Kubernetes 工作节点 |

初次检查时所有主机以 `root`、SSH 端口 `22` 录入。2026-08-24 前台检查结果为 `8/8` 个端点可达：6 台主机、镜像仓库和 File Station 均通过 TCP 检查。

## Kubernetes 测试集群 2

ClusterForge 环境名称：`Kubernetes 测试集群 2`。2026-08-24 初次前台检查时观察到 Revision `r3`，架构为 `amd64`，操作系统记录为 `Ubuntu 18.04`，网络栈为 `IPv4`；同日后续验收把规范化事实保存为 `r6`，以链接的验收记录为准。

| IP | MAC | 节点名 | Inventory 分组 | 用途 |
| --- | --- | --- | --- | --- |
| `192.168.88.59` | `00:15:5D:58:8F:03` | master-4 | `k8s_cert_controller`, `k8setcd`, `k8smaster`, `k8s_F5` | 证书控制节点、etcd、Kubernetes 控制节点、API 入口 |
| `192.168.88.56` | `00:15:5D:58:8F:04` | master-5 | `k8setcd`, `k8smaster` | etcd、Kubernetes 控制节点 |
| `192.168.88.58` | `00:15:5D:58:8F:05` | master-6 | `k8setcd`, `k8smaster` | etcd、Kubernetes 控制节点 |
| `192.168.88.67` | `00:15:5D:58:26:07` | node-4 | `k8snode` | Kubernetes 工作节点 |
| `192.168.88.68` | `00:15:5D:58:26:08` | node-5 | `k8snode` | Kubernetes 工作节点 |
| `192.168.88.69` | `00:15:5D:58:26:09` | node-6 | `k8snode` | Kubernetes 工作节点 |

初次检查时所有主机以 `root`、SSH 端口 `22` 录入。2026-08-24 前台检查结果为 `8/8` 个端点可达：6 台主机、镜像仓库和 File Station 均通过 TCP 检查。

## 2026-08-24 清理后的基线

两套 Kubernetes Inventory 的 12 台主机已清理为可重新安装的测试基线：

- Kubernetes、etcd、Flannel 服务、进程、配置和状态目录已清除。
- Docker、Containerd、runc、容器、节点本地镜像和数据目录已删除。
- `docker0`、`cni0`、`flannel.1` 等测试网络接口已删除。
- IPv4 和 IPv6 iptables 规则已清空，内置链默认策略为 `ACCEPT`；iptables 工具本身仍保留。
- 操作系统、主机名、SSH、网络配置和基础账号保留。

基础服务的保留边界：

- `192.168.88.54` 的镜像仓库及其中镜像保留。
- `192.168.88.55` 的平台程序、用户、组件目录、场景、环境及 Revision 保留；运行、安装、健康检查、构建记录和运行时临时目录已清空后重新产生本次环境检查记录。
- `192.168.88.57` 的 File Station 服务和介质保留。
- 12 台 Kubernetes 节点本地的 Docker 镜像与容器数据未备份，不能从节点本机恢复；需要时从 `192.168.88.54:5000` 重新拉取。

清理前备份位置：

| 范围 | 远端备份目录 | 内容 |
| --- | --- | --- |
| 12 台 Kubernetes 节点 | `/var/backups/clusterforge-safe-clear/20260824T043708Z` | Kubernetes/etcd 配置与状态、Docker 配置清单、清理前 iptables 规则及操作标记 |
| `192.168.88.55` 平台 | `/var/backups/clusterforge-safe-clear/20260824T043708Z-platform` | SQLite 数据库和运行时目录备份 |

## 前台维护路径

使用 `王维 · 基础设施`（Environment Owner）身份进入：

1. `环境管理` → 选择目标环境 → `Inventory`：维护主机地址、分组、SSH 用户和端口。
2. `环境管理` → 选择目标环境 → `环境变量`：维护 `IMAGE_REGISTRY` 和 `FILE_STATION`。
3. 保存时填写变更原因并创建新的 Environment Revision，不覆盖历史 Revision。
4. 点击 `立即检查` 只验证 TCP 连通性；安装前仍需确认 SSH 凭据、Ansible 依赖、介质完整性和活动 Run。
