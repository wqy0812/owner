# NewPlatform Demo

当前执行契约：场景编译为单个 Ansible 作业，组件使用独立 Role。参见[组件编写规范](docs/component-role-authoring.md)、[场景执行](docs/scenario-role-job.md)及[独立作业包](docs/standalone-role-job.md)。新库只初始化身份和平台目录；旧作业快照不自动装入。

> 当前是项目首个版本（V1），当前环境仅为测试环境，不是生产环境。V1 只接受当前数据库合同，不提供运行时历史迁移、旧字段或双合同兼容；历史离线转换入口已删除，其他合同全部失败关闭。详见 [首版与环境策略](docs/version-policy.md)。

一个用于管理 Ansible 组件、场景和测试环境的本地演示平台。后端使用 Go + SQLite，前端使用 React + TypeScript，提供按角色切换的 Demo 身份，并支持受控的真实 `ansible-playbook` 执行。

## 能力

- 组件 Owner：按 L1-L6 维护组件分类、结构化合同、不可变发布版本和当前交付证据；Draft 可共享到候选集，Playbook 支持上传和在线编辑。
- 集群 Owner：使用 DAG 组合精确 Released/候选组件版本，完整测试后原子发布场景 Revision 与全部候选 Release。
- 环境 Owner：管理 Inventory、`IMAGE_REGISTRY` / `FILE_STATION` 等非敏感环境变量与凭据引用，执行整集群回滚预览并单条或批量审批高风险作业。
- 平台 Owner：维护平台目录、在工作台审核当前组件合同并管理 Run 归档与保留策略。
- 共享测试环境：单环境 FIFO 执行、日志搜索/流筛选/复制/下载、取消、审计和站内通知。
- 当前参考 Playbook：[Kubernetes 1.17.5 原生组件与场景](examples/ansible/kubernetes-1.17.5/README.md)，依据 2026-09-08 测试平台发布的 15 个组件与一个场景整理；固定 Ansible 2.8.8，来源证据与重构后验证分开记录。
- 测试目录历史快照（2026-08-30）：15 个已发布组件 Release、1 个已发布 Kubernetes 1.17.5 场景 Revision 和 2 个测试环境；使用前需重新核对，精确清单及复现方法见 [测试环境资产目录](docs/demo-catalog.md)。
- API 错误统一为 `{error:{code,message,details}}`；运行锁定组件、场景、环境 Revision 与 Playbook 树摘要。

## 文档

- [文档中心与维护索引](docs/README.md)
- [开发与文档维护指南](docs/development-guide.md)
- [平台功能与核心概念](docs/platform-capabilities.md)
- [项目结构说明](docs/project-structure.md)
- [平台设计文档（后端为主）](docs/backend-design.md)
- [Run 活动读取、场景保存基线与工作台查询](docs/run-activity-and-workbench.md)
- [平台说明书](docs/platform-manual.md)
- [场景分支、版本演进与业务验收](docs/scenario-lifecycle.md)
- [测试环境资产目录](docs/demo-catalog.md)

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

前端面向桌面浏览器，工作区最小宽度为 1280 CSS 像素，较小窗口横向滚动；保留手动侧栏收起和表格滚动，不维护移动端布局。隔离启动步骤见 [开发指南](docs/development-guide.md)。

前端开发地址为 `http://127.0.0.1:5173`，Vite 会把 `/api` 代理到默认后端 `http://127.0.0.1:8080`。

首次启动会幂等写入演示用户、组件、场景和环境。也可以单独运行：

```bash
make seed
```

## 测试与构建

```bash
make check-docs
make test-fast
make test-local
make test-deploy  # make test is an alias; uses the pinned Docker runtime
make test-e2e
make build
```

`make build` 先构建前端，再生成嵌入静态资源的 `bin/newplatform`、备份工具 `bin/clusterforge-backup` 和独立作业工具 `bin/clusterforge-job`。

`test-fast` 运行快速检查；`test-local` 运行部署脚本、夹具、证据、文档、Go、前端覆盖率及隔离 API 浏览器检查。统一部署门禁为 **`make test-deploy`（`make test` 同义）**：公共检查通过后，在隔离 Docker 中执行原生 Role、SSH 镜像前置探针、当前参考 Playbook 和真实 SSH 冒烟。88.55 与本地 Docker 部署均调用此入口，任一阶段失败、缺少固定运行时、Role 用例跳过或源码变化均阻断部署。命令、日志、镜像 ID 和源码摘要保存在 `output/deployment-gate/<时间-标识>/`。

本轮交付与验证边界见 [2026-09-07 分批审核记录](docs/records/2026-09-07-batched-code-review.md)。

## 部署到本地 Docker

更新现有 `clusterforge-test-ubuntu` 容器，包含当前工作区未提交改动：

```bash
./scripts/deploy-local-docker.sh
```

固定使用 **Ansible 2.8.8 / Python 3.6.9 / Ubuntu 18.04**，访问地址为
`http://127.0.0.1:8080`，SSH 保持本机 `22222` 端口。以后本地涉及 Ansible 的测试
均在这个运行时中执行。脚本要求已建立该本地环境及其 Compose 配置；具体流程见
[开发指南](docs/development-guide.md#本地-docker-部署与-ansible-测试)。

脚本先复制并构建当前工作区，默认执行 Go、前端与隔离容器内的真实 Ansible
回归，再备份数据库、切换镜像并校验 HTTP、静态资源摘要和执行器。
已有数据卷保留；活动工作、合同不匹配或源码变化会阻断部署。失败时回退原镜像，
保留当前数据，数据库备份另存供需要时恢复。不会提交、推送或重建数据库。

```bash
./scripts/deploy-local-docker.sh --check       # 只检查当前环境
./scripts/deploy-local-docker.sh --skip-tests  # 跳过项目回归，保留必需部署检查
make test-deploy-local-docker                 # 脚本的隔离门禁测试
```

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

当前合同为 `clusterforge-v1-20260908-typed-run-snapshot`。服务、业务导出和基础目录重置只接受该精确合同；运行时不兼容旧 Run 快照。上一合同的数据通过 `database migrate-run-snapshot` 一次性离线转换到独立新库并验证，完整流程及受保护切换见 [Run 快照合同与离线迁移](docs/design/run-snapshot-contract.md)。转换保留 Run、基线、证据与归档，不需要清空业务库。`database foundation-snapshot` 只把当前合同的账号与基础目录复制到新库，见[业务重置流程](docs/catalog-backup-and-restore.md#业务清空专用备份不含-run-历史)。部署检查与 SQLite 文件备份使用 `clusterforge-backup database` 内置的 Go SQLite 引擎。

只有明确需要重建不兼容测试库时才使用 `--rebuild-v1-db`：重建不允许存在活动 Run，脚本会先保存二进制、环境配置，并生成经过完整性与外键检查的一致 SQLite 备份；已配置 Catalog 仓库时还要求部署前快照成功。启动、HTTP、结构合同、外键、静态资源摘要或重建后快照检查失败会恢复原二进制和数据库。不要在生产或需要保留历史的环境使用该开关。

`make test-deploy` 要求本机 Docker 已具有 `clusterforge-test-ubuntu:18.04-ansible2.8.8` 镜像，并核验 Ansible 2.8.8 / Python 3.6.9。独立运行和 88.55 部署创建一次性容器，只复用 Go 缓存；本地部署复用本次隔离候选容器。门禁不挂载平台业务数据，不执行上传或激活。定点诊断仍可在固定容器中执行 `make test-role-job ANSIBLE_PLAYBOOK=/opt/ansible/bin/ansible-playbook`；它检查四个包的真实用例，拒绝跳过或空选择。现有 `--skip-tests` 显式跳过整个回归门禁，不能当作门禁通过，源码、数据、产物与激活检查仍保留。

门禁中的浏览器证据保存在同次门禁的 `browser/` 子目录。本地 Docker 部署将整个门禁证据写入 `output/local-docker-deploy/<标识>/gate/`，临时源码副本清理后日志、API 结果、截图及失败 trace 仍保留。


## 组件分层

组件按 L1 主机基础与安全、L2 运行时与状态存储、L3 Kubernetes 编排核心、L4 集群网络/服务发现/存储、L5 可观测与节点管理、L6 平台扩展分组，并可填写少量自由标签用于检索。环境架构、操作系统、IP 协议族等仍属于 Release 的环境约束；调度与依赖不从标签推导。

分层用于目录展示、检索和编排提示，不代表精确执行顺序。Release 依赖锁定精确上游版本并自动生成只读依赖边；场景 Owner 可添加手工顺序边，两类边共同决定实际拓扑和执行顺序。

## Kubernetes 1.17.5 测试目录快照（2026-08-30）

该日期的测试环境保存细粒度已发布组件，覆盖主机预检与初始化、PKI、加密配置、Docker、etcd、Kubernetes 控制面与节点进程、Flannel 和 CoreDNS。已发布场景“Kubernetes 1.17.5 Ubuntu 六节点细粒度集群”为 r2，包含 21 个节点和 50 条依赖边。需要同时覆盖控制节点和工作节点的组件由组件 Owner 提供独立 Release 发布线，场景只选择精确 Release 和 Action，主机组不可改写。

两套环境均为 Ubuntu 测试节点，使用 `IMAGE_REGISTRY`、`FILE_STATION` 和 `K8S_ENCRYPTION_KEY` CredentialRef。组件、Release、动作、场景 DAG、Environment Revision、Inventory 和恢复点的精确值见 [测试环境资产目录](docs/demo-catalog.md)。文档中的 TCP 或 SSH 连通性证据不等于 Kubernetes 安装和收敛验收。

## Ansible 安全边界

- 只执行 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 下的相对 Playbook，拒绝路径穿越。
- 平台按 `managed/<component-slug>/<release-line-key>/<version-key>--<release-id>/` 建立版本级 Ansible 工作区；完整 Release ID 防止规范化后同名版本发生目录碰撞。Action 入口固定为 `<actionKind>.yml`，组件 Owner 不填写服务器路径或文件名。
- 工作区可包含 `roles/`、`templates/`、`files/` 和其他辅助目录。单文件 10 MiB、单版本 50 MiB、1000 文件、16 层；UTF-8 文本不超过 1 MiB 时可在线编辑，符号链接、设备文件和路径穿越一律拒绝。安装包、离线包等大型介质仍走 FSS。
- Release 合同锁定工作区全量文件清单和树摘要；任意模板、Role、脚本或小型二进制变化都会撤销 Draft 审核/候选状态并使旧测试证据失效。Runner 每次只复制目标 Release 工作区。
- Action 保存和删除同时提交完整动作元数据、固定入口文件、工作区清单与合同失效；数据库事务失败时恢复原入口字节，进程中断后启动恢复日志也会完成回滚，不留下半个 Action。
- 变更类动作逐步骤使用隔离快照；同一 Release 的只读 Verify/Inspect 可在一次 Run 内复用已校验快照。每步结束后重新核对目录树摘要；Inventory/变量文件保持 0600，并使用独立 `ANSIBLE_LOCAL_TEMP`。
- Secret 运行时解析并脱敏，不写入数据库、快照或保留日志。
- 每个环境同时只有一个活动执行；其他请求 FIFO 排队。
- 安装备份按环境、组件、Release 和安装 Run 隔离；回滚只消费环境当前安装记录
  的 `backup_ref`，拒绝元数据或 Playbook 哈希不匹配的旧基线。
- `recovery`、`clean`、`destroy`、`uninstall` 以及显式 destructive 动作必须由环境 Owner 审批。

## 主要配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEWPLATFORM_ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `NEWPLATFORM_DB_PATH` | `./data/newplatform.db` | SQLite 文件 |
| `NEWPLATFORM_ANSIBLE_BIN` | `ansible-playbook` | Ansible 可执行文件 |
| `NEWPLATFORM_RUN_ROOT` | `./data/runs` | Run 临时工作区 |
| `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` | `./examples/ansible` | 允许执行的作业根目录；Demo 使用首个配置项 |
| `NEWPLATFORM_SSH_KNOWN_HOSTS` | 服务账号的 `~/.ssh/known_hosts` | Go SSH 环境检查使用的严格主机指纹文件 |
| `NEWPLATFORM_KILL_GRACE` | `3s` | 取消后进程组强制终止宽限期 |
| `NEWPLATFORM_MAX_LOG_BYTES` | `2097152` | 单个 Ansible step 保留的脱敏日志上限 |
| `NEWPLATFORM_SEED_PROFILE` | `demo` | `identities` 创建角色切换账号，并仅在新库初始化平台治理目录；组件、场景和环境目录保持为空供人工录入 |
| `NEWPLATFORM_IMAGE_BUILD_ROOT` | `./data/image-builds` | 单 Dockerfile 隔离构建上下文的临时根目录 |
| `NEWPLATFORM_DOCKER_BIN` | `docker` | 构建和推送镜像所用的 Docker CLI |
| `CLUSTERFORGE_RUN_ARCHIVE_DIR` | 未配置 | 独立持久化 Run 归档目录；完整历史备份须包含此目录，见[运行历史管理](docs/run-history-and-adaptation.md) |
| `CLUSTERFORGE_BACKUP_ENABLED` | `false` | 启用发布/废弃后的异步 SQLite 与 Git Catalog 快照；部署环境显式设为 `true` |
| `CLUSTERFORGE_BACKUP_DIR` | `./data/catalog-backups` | SQLite 快照、校验清单和最近成功恢复点目录 |
| `CLUSTERFORGE_CATALOG_REPO` | `./data/catalog-repo` | 仅供离线 CLI 使用的默认 Catalog 工作副本；在线备份使用前台选择 |
| `CLUSTERFORGE_CATALOG_REMOTE` | `origin` | Catalog 推送远端 |
| `CLUSTERFORGE_CATALOG_BRANCH` | `catalog` | 最新完整 Catalog 所在的 fast-forward-only 分支 |
| `CLUSTERFORGE_CATALOG_ALLOWED_ROOT` | `./data/private-catalog-repositories` | 环境 Owner 可创建或接入本地私有仓库的受控根目录 |
| `CLUSTERFORGE_BACKUP_DEBOUNCE` | `30s` | 连续发布合并为一次异步快照的等待窗口 |
| `NEWPLATFORM_K8S1175_ENCRYPTION_KEY` | 无 | 批准执行 Kubernetes 1.17.5 作业时必需；32 字节密钥的 base64 值，仅以 CredentialRef 注入 |

环境 Owner 在环境页面维护非敏感大写环境变量；每次保存都会生成新的
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

发布目录与 SQLite 的异步快照、前台立即备份、Git 分支/标签语义和两级恢复步骤见 [Catalog 与数据库备份恢复](docs/catalog-backup-and-restore.md)。

适配标签、直接引用查询、Run 归档与保留策略见 [运行历史与适配标签](docs/run-history-and-adaptation.md)。
