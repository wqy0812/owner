# NewPlatform Demo

> 当前是项目首个版本（V1），当前环境仅为测试环境，不是生产环境。V1 不提供通用历史兼容；仅代码显式列出的精确前序 V1 数据库合同可执行经过测试的加法迁移，未知合同失败关闭。API 不接受旧字段或双合同。详见 [首版与环境策略](docs/version-policy.md)。

一个用于管理 Ansible 组件、场景和测试环境的本地演示平台。后端使用 Go + SQLite，前端使用 React + TypeScript，内置四个可切换的 Demo 身份，并支持受控的真实 `ansible-playbook` 执行。

## 能力

- 组件 Owner：按 L1-L6 维护组件分类、结构化合同、不可变发布版本和当前交付证据；Draft 可共享到候选集，Playbook 支持上传和在线编辑。
- 场景 Owner：使用 DAG 组合精确 Released/候选组件版本，完整测试后原子发布场景 Revision 与全部候选 Release。
- 环境 Owner：管理 Inventory、`IMAGE_REGISTRY` / `FILE_STATION` 等非敏感环境变量与凭据引用，执行整集群回滚预览并单条或批量审批高风险作业。
- 共享测试环境：单环境 FIFO 执行、日志搜索/流筛选/复制/下载、取消、审计和站内通知。
- 示例：3 个 OpenFuyao 场景、2 个 Kubernetes 1.17.5 场景及对应测试环境模板。
- API 错误统一为 `{error:{code,message,details}}`；运行锁定组件、场景、环境 Revision 与 Playbook 树摘要。

## 文档

- [文档中心与维护索引](docs/README.md)
- [项目结构说明](docs/project-structure.md)
- [平台设计文档（后端为主）](docs/backend-design.md)
- [平台操作手册（分角色）](docs/operation-manual.md)
- [Demo 资产目录](docs/demo-catalog.md)

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

## 部署到 192.168.88.55 测试环境

部署当前工作区（包括未提交改动）：

```bash
./scripts/deploy-test-88-55.sh
```

脚本默认执行 Go/React 测试和 `git diff --check`，构建嵌入前端的 Linux amd64
二进制，然后通过 SSH 部署到 `root@192.168.88.55`。切换前会检查活动 Run，备份
现有二进制、SQLite 数据库和环境配置；服务未在 15 秒内恢复时会自动回退。
前台构建会同时生成不缓存的 `version.json`；已经加载版本守卫的 SPA 会在后续部署后阻断旧页面操作并提示刷新。

可选参数：

```bash
./scripts/deploy-test-88-55.sh --help
./scripts/deploy-test-88-55.sh --skip-tests
./scripts/deploy-test-88-55.sh --target root@192.168.88.55 --ssh-port 22
./scripts/deploy-test-88-55.sh --rebuild-v1-db
```

只有明确接受中断活动 Run 时才使用 `--allow-active-runs`。

当前合同为 `clusterforge-v1-20260828-environment-lifecycle`，包含模块化发布围栏以及环境删除/归档状态，按 V1 决策不提供旧字段迁移。首次把该合同部署到已有测试库时必须显式使用 `--rebuild-v1-db`：脚本会先检查活动 Run 并备份二进制、SQLite 和环境配置，停服务后才删除工作库并由新版本重建/Seed；启动、HTTP、结构合同或外键检查失败会恢复原二进制和数据库。不要在生产或需要保留历史的环境使用该开关。

`make test` 会执行 Go/React 测试，并用测试运行时生成的临时 Playbook 验证真实 `ansible-playbook` 进程。该夹具只写入测试专用临时目录，不作为平台组件、场景或环境保存。

## 组件分层

组件按 L1 主机基础与安全、L2 运行时与状态存储、L3 Kubernetes 编排核心、L4 集群网络/服务发现/存储、L5 可观测与节点管理、L6 平台扩展分组，并可填写少量自由标签用于检索。环境架构、操作系统、IP 协议族等仍属于 Release 的环境约束；调度与依赖不从标签推导。

分层用于目录展示、检索和编排提示，不代表精确执行顺序。Release 依赖锁定精确上游版本，场景 DAG 的边决定实际拓扑和执行顺序。

## Kubernetes 1.17.5 集群搭建作业

Demo 会幂等写入核心 `scenario-k8s-1.17.5`、扩展 `scenario-k8s-1.17.5-extended` 及 `environment-k8s-1.17.5-template`。首版模型使用最小逻辑组件和不可变 Release；同一 Release 可在控制节点和工作节点各有一个场景节点，由节点 `hostGroup` 区分。

场景保持 Draft，以免把尚未在真实目标环境验收的快照误标为已发布；在页面切换到“陈晨 · 集群交付”身份即可查看和发起环境测试。

| 层 | 核心能力 | 版本 |
| --- | --- | --- |
| L1 | Host Preflight、Host Bootstrap、Cluster PKI、Encryption Configuration | `v1.17.5-r1` |
| L2 | Docker、etcd | `18.09.7`、`3.3.10` |
| L3 | Distribution、三个控制面进程、Bootstrap RBAC、kubelet、kube-proxy | `1.17.5` |
| L4 | Flannel、CoreDNS | `0.11.0`、`1.3.1` |

Host Preflight 是只读动作；其余写主机或集群状态的动作均为 destructive，发起后必须由对应环境 Owner 审批。每个组件在 `k8s-1.17.5-cluster/components/` 下都有独立主入口和验证入口，组件测试会自动追加 Verify。恢复、卸载、Housekeeping 以及只有模板没有任务入口的 process-exporter、Event Monitor、CSI 不进入安装场景。

加密密钥值不进入 Seed、数据库或作业快照；环境模板通过 CredentialRef 将 Ansible 变量 `K8S_ENCRYPTION_KEY` 指向后端进程环境变量 `NEWPLATFORM_K8S1175_ENCRYPTION_KEY`。部署方必须提供一个经审核的 32 字节密钥的 base64 值。当前 Kubernetes 1.17.5 Action 尚未把该引用声明为 `requiredCredentials`，因此环境 Owner 在审批前必须人工确认引用和后端变量均已配置，并依赖 Playbook 预检失败关闭。

所有外部压缩包/二进制都要求 64 位十六进制 SHA256，容器镜像要求 `sha256:` digest；模板中的未知值保持为空，组件预检会在任何远端写操作前失败。无法从源快照确认软件版本的附加 Release 使用 `source-6909da3`，不推测上游版本；真实就绪结论由当前合同和双证据派生的 Readiness 表达。

扩展场景完整包含核心 DAG，并追加 Node Logging、HAProxy、Blackbox/Node Exporter、Metrics Server、AMC、GlusterFS Client、Go/pprof、Prometheus Access 和 Autoscaling RBAC。快照位于 `examples/ansible/k8s-1.17.5-cluster`；本地语法、任务枚举和代码测试通过，不等于真实 SUSE 三控制节点加工作节点的安装、介质验真和集群收敛验收。

## Ansible 安全边界

- 只执行 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 下的相对 Playbook，拒绝路径穿越。
- 前台上传/在线编辑的 Playbook 限 1 MiB UTF-8 YAML，并写入 `managed/<component-slug>/<release-id>/`；已发布版本不可改，内容变化会使原测试验证失效。
- 每次运行使用独立工作区、0600 Inventory/变量文件和独立 `ANSIBLE_LOCAL_TEMP`。
- Secret 运行时解析并脱敏，不写入数据库、快照或保留日志。
- 每个环境同时只有一个活动执行；其他请求 FIFO 排队。
- 安装备份按环境、组件、Release 和安装 Run 隔离；回滚只消费环境当前安装记录
  的 `backup_ref`，拒绝元数据或 Playbook 哈希不匹配的旧基线。
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
| `NEWPLATFORM_SEED_PROFILE` | `demo` | `identities` 仅创建角色切换账号，目录保持为空供人工录入 |
| `NEWPLATFORM_IMAGE_BUILD_ROOT` | `./data/image-builds` | 单 Dockerfile 隔离构建上下文的临时根目录 |
| `NEWPLATFORM_DOCKER_BIN` | `docker` | 构建和推送镜像所用的 Docker CLI |
| `CLUSTERFORGE_BACKUP_ENABLED` | `false` | 启用发布/废弃后的异步 SQLite 与 Git Catalog 快照；部署环境显式设为 `true` |
| `CLUSTERFORGE_BACKUP_DIR` | `./data/catalog-backups` | SQLite 快照、校验清单和最近成功恢复点目录 |
| `CLUSTERFORGE_CATALOG_REPO` | `./data/catalog-repo` | 仅供离线 CLI 使用的默认 Catalog 工作副本；在线备份使用前台选择 |
| `CLUSTERFORGE_CATALOG_REMOTE` | `origin` | Catalog 推送远端 |
| `CLUSTERFORGE_CATALOG_BRANCH` | `catalog` | 最新完整 Catalog 所在的 fast-forward-only 分支 |
| `CLUSTERFORGE_CATALOG_ALLOWED_ROOT` | `./data/private-catalog-repositories` | Environment Owner 可创建或接入本地私有仓库的受控根目录 |
| `CLUSTERFORGE_BACKUP_DEBOUNCE` | `30s` | 连续发布合并为一次异步快照的等待窗口 |
| `CLUSTERFORGE_SYSTEMCTL_BIN` | `systemctl` | 对齐六小时备份 Timer 状态使用的 systemctl 路径 |
| `CLUSTERFORGE_BACKUP_TIMER_UNIT` | `clusterforge-backup.timer` | 六小时备份 Timer 单元名 |
| `NEWPLATFORM_K8S1175_ENCRYPTION_KEY` | 无 | 批准执行 Kubernetes 1.17.5 作业时必需；32 字节密钥的 base64 值，仅以 CredentialRef 注入 |

Environment Owner 在环境页面维护非敏感大写环境变量；每次保存都会生成新的
Environment Revision。变量会以同名 Ansible extra-vars 注入组件作业，例如
Playbook 可直接使用 `{{ IMAGE_REGISTRY }}`。变量名与组件参数或 CredentialRef
冲突时，平台会在创建 Run 前拒绝执行。

组件 Owner 可在 Draft 发布行选择“构建镜像”，选择一个已配置
`IMAGE_REGISTRY` 的环境，再上传不超过 1 MiB 的单个 Dockerfile。平台锁定所选
Environment Revision，并推送到 `IMAGE_REGISTRY/components/<slug>:<tag>`；之后
环境变量变化不会改写已提交构建。平台不会接收本地构建目录或主机路径；构建、
推送日志和最终 RepoDigest 会记录在数据库中。Dockerfile 会由平台主机 Docker
daemon 执行，因此只应上传可信内容。

## 灾备

发布目录与 SQLite 的异步快照、人工 CLI、Git 分支/标签语义和两级恢复步骤见 [Catalog 与数据库备份恢复](docs/catalog-backup-and-restore.md)。

## 示例来源

`examples/ansible/openfuyao` 是当前仓库外现有作业的脱敏快照；来源 commit、文件数量和树摘要记录在该目录的 `SOURCE.md`。它不会自动与源仓库同步。
