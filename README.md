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

Demo 会幂等写入 `scenario-k8s-1.17.5` 场景及 `environment-k8s-1.17.5-template` 环境模板。场景锁定四个组件版本，并按三阶段四步执行：

场景保持 Draft，以免把尚未在真实目标环境验收的快照误标为已发布；在页面切换到“陈晨 · 集群交付”身份即可查看和发起环境测试。

| 阶段 | 步骤 | Playbook |
| --- | --- | --- |
| 证书 | 生成集群证书 | `k8s-1.17.5-cluster/cert_1175.yml` |
| 控制面 | 安装 etcd | `k8s-1.17.5-cluster/etcd_serverless.yml` |
| 控制面 | 安装 Kubernetes master | `k8s-1.17.5-cluster/master_1175.yml` |
| 工作节点 | 安装 Kubernetes node | `k8s-1.17.5-cluster/node_1175.yml` |

该场景的所有步骤均为 destructive。发起测试或正式运行后，作业会停留在 `awaiting_approval`，只有对应环境 Owner 批准后才会调用 Ansible。四个 Action 固定传入 `--tags install`；快照已断开 Docker 卸载重装、4243 防火墙、journal 清空和会向容器分发 API Server 私钥的历史 Housekeeping 分支，且不会执行仅标记为 recovery 的任务。它也不会改写 zypper 软件源或安装未验签 Docker 包，要求环境预置并显式验真 Docker 版本。环境模板只是脱敏占位，不能直接连接真实主机；正式批准前必须替换并复核 Inventory、主机分组、网络参数和介质服务地址。

加密密钥值不进入 seed、数据库或作业快照；数据库仅保存非敏感的引用元数据。环境模板通过动态 CredentialRef 将 Ansible 变量 `K8S_ENCRYPTION_KEY` 指向后端进程环境变量 `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`；部署方必须提供一个经审核的 32 字节密钥的 base64 值。Runner 只在批准后的执行阶段解析并注入该值。

原始 1.17.5 role 使用历史变量名 `K8SMASTER_1173_CERT` 和 `K8SNODE_1173_CERT` 下载 master/node 介质，但源仓库没有证明这些值就是目标 1.17.5 包。Demo 不延续这个易误导的名称，已将快照合同安全适配为 `K8SMASTER_1175_CERT` 和 `K8SNODE_1175_CERT`；preflight 要求两个路径显式包含 `1.17.5`，同时要求 `K8S1175_ARTIFACTS_VERIFIED=true`。部署方仍必须独立核对介质版本与校验和；任一条件不满足都应阻断执行。Demo 不携带源 `group_vars` 或内部介质。

快照位于 `examples/ansible/k8s-1.17.5-cluster`。它排除了源 Inventory、`group_vars`、私钥、既有 CA 证书与私钥、静态 encryption config 和 JupyterHub 清单；CA 按 `CLUSTER_ID` 在运行时生成，master 的 encryption config 由上述 CredentialRef 动态渲染。为适配现代 Ansible，旧式任务 `include` 已改为静态 `import_tasks`，变量加载仍使用 `include_vars`。这些改动只完成了 Demo 快照与执行入口适配，尚未在真实 Kubernetes 1.17.5 目标环境完成安装验收。

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
