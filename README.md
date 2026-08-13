# NewPlatform Demo

一个用于管理 Ansible 组件、场景和测试环境的本地演示平台。后端使用 Go + SQLite，前端使用 React + TypeScript，内置四个可切换的 Demo 身份，并支持受控的真实 `ansible-playbook` 执行。

## 能力

- 组件 Owner：按 L1-L6 维护组件分类、不可变发布版本、依赖、动作与影响通知。
- 场景 Owner：使用 DAG 组合精确组件版本，测试通过后发布场景。
- 环境 Owner：管理 Inventory、环境参数与凭据引用，审批高风险作业。
- 共享测试环境：单环境 FIFO 执行、实时日志、取消、审计和站内通知。
- 示例：OpenFuyao 管理集群和 Kubernetes 1.17.5 集群搭建作业快照。
- API 错误统一为 `{error:{code,message,details}}`；运行锁定组件、场景、环境 Revision 与 Playbook 树摘要。

## 文档

- [平台设计文档（后端为主）](docs/backend-design.md)
- [平台操作手册（分角色）](docs/operation-manual.md)

> 这是本地 Demo，不是生产控制面。身份切换不包含密码认证；凭据仅允许保存文件路径或环境变量引用。

## 前置条件

- Go 1.25+
- Node.js 22+ 和 pnpm 10+
- Ansible（真实执行或集成测试需要）

## 快速开始

```bash
cp .env.example .env
make bootstrap
make dev
```

前端开发地址为 `http://127.0.0.1:5173`，Vite 会把 `/api` 代理到默认后端 `http://127.0.0.1:8080`。

首次启动会幂等写入演示用户、组件、场景和环境。也可以单独运行：

```bash
make seed
```

## 测试与构建

```bash
make test
make test-e2e
make build
```

`make build` 先构建前端，再将静态资源嵌入 `bin/newplatform`。

`make test` 会执行 Go/React 测试，并用测试运行时生成的临时 Playbook 验证真实 `ansible-playbook` 进程。该夹具只写入测试专用临时目录，不作为平台组件、场景或环境保存。

## 组件分层

组件按 L1 主机基础与安全、L2 运行时与状态存储、L3 Kubernetes 编排核心、L4 集群网络/服务发现/存储、L5 可观测与节点管理、L6 平台扩展分组。每个组件同时记录能力类别、组件形态和必选性；环境架构、操作系统、IP 协议族等仍属于 Release 的环境约束。

分层用于目录展示、检索和编排提示，不代表精确执行顺序。Release 依赖锁定精确上游版本，场景 DAG 的边决定实际拓扑和执行顺序。

## Kubernetes 1.17.5 集群搭建作业

Demo 会幂等写入核心 `scenario-k8s-1.17.5`、扩展 `scenario-k8s-1.17.5-extended` 及 `environment-k8s-1.17.5-template`。旧的 Certificates、Etcd、Control Plane、Worker 四个版本化交付阶段已删除，改为最小逻辑组件和不可变 Release；同一 Release 可在控制节点和工作节点各有一个场景节点，由节点 `hostGroup` 区分。

场景保持 Draft，以免把尚未在真实目标环境验收的快照误标为已发布；在页面切换到“陈晨 · 集群交付”身份即可查看和发起环境测试。

| 层 | 核心能力 | 版本 |
| --- | --- | --- |
| L1 | Host Preflight、Host Bootstrap、Cluster PKI、Encryption Configuration | `v1.17.5-r1` |
| L2 | Docker、etcd | `18.09.7`、`3.3.10` |
| L3 | Distribution、三个控制面进程、Bootstrap RBAC、kubelet、kube-proxy | `1.17.5` |
| L4 | Flannel、CoreDNS | `0.11.0`、`1.3.1` |

Host Preflight 是只读动作；其余写主机或集群状态的动作均为 destructive，发起后必须由对应环境 Owner 审批。每个组件在 `k8s-1.17.5-cluster/components/` 下都有独立主入口和验证入口，组件测试会自动追加 Verify。恢复、卸载、Housekeeping 以及只有模板没有任务入口的 process-exporter、Event Monitor、CSI 不进入安装场景。

加密密钥值不进入 seed、数据库或作业快照；数据库仅保存非敏感的引用元数据。环境模板通过动态 CredentialRef 将 Ansible 变量 `K8S_ENCRYPTION_KEY` 指向后端进程环境变量 `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`；部署方必须提供一个经审核的 32 字节密钥的 base64 值。Runner 只在批准后的执行阶段解析并注入该值。

所有外部压缩包/二进制都要求 64 位十六进制 SHA256，容器镜像要求 `sha256:` digest；模板中的未知值保持为空，组件预检会在任何远端写操作前失败。无法从源快照确认软件版本的附加 Release 使用 `source-6909da3` 且 `verified=false`，不推测上游版本。

扩展场景完整包含核心 DAG，并追加 Node Logging、HAProxy、Blackbox/Node Exporter、Metrics Server、AMC、GlusterFS Client、Go/pprof、Prometheus Access 和 Autoscaling RBAC。快照位于 `examples/ansible/k8s-1.17.5-cluster`；本地语法、任务枚举和代码测试通过，不等于真实 SUSE 三控制节点加工作节点的安装、介质验真和集群收敛验收。

## Ansible 安全边界

- 只执行 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 下的相对 Playbook，拒绝路径穿越。
- 每次运行使用独立工作区、0600 Inventory/变量文件和独立 `ANSIBLE_LOCAL_TEMP`。
- Secret 运行时解析并脱敏，不写入数据库、快照或保留日志。
- 每个环境同时只有一个活动执行；其他请求 FIFO 排队。
- `recovery`、`clean`、`destroy`、`uninstall` 以及显式 destructive 动作必须由环境 Owner 审批。
- OpenFuyao build 包含 `rcv`，因此默认被视为 destructive；没有合格主机、内部介质和仓库时不要批准执行。

## 主要配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEWPLATFORM_ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `NEWPLATFORM_DB_PATH` | `./data/newplatform.db` | SQLite 文件 |
| `NEWPLATFORM_ANSIBLE_BIN` | `ansible-playbook` | Ansible 可执行文件 |
| `NEWPLATFORM_RUN_ROOT` | `./data/runs` | 每次运行的临时工作区根目录 |
| `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` | `./examples/ansible` | 允许执行的作业根目录；Demo 使用首个配置项 |
| `NEWPLATFORM_KILL_GRACE` | `3s` | 取消后进程组强制终止宽限期 |
| `NEWPLATFORM_MAX_LOG_BYTES` | `2097152` | 单个 Ansible step 保留的脱敏日志上限 |
| `NEWPLATFORM_K8S1175_ENCRYPTION_KEY` | 无 | 批准执行 Kubernetes 1.17.5 作业时必需；32 字节密钥的 base64 值，仅以 CredentialRef 注入 |

## 示例来源

`examples/ansible/openfuyao` 是当前仓库外现有作业的脱敏快照；来源 commit、文件数量和树摘要记录在该目录的 `SOURCE.md`。它不会自动与源仓库同步。
