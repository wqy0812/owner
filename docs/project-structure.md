# ClusterForge 项目结构说明

> 版本与环境：本文属于项目首个版本（V1）；当前环境是测试环境，不是生产环境。V1 只接受当前精确合同，不提供历史迁移或双合同兼容；其他合同失败关闭。统一规则见 [首版与环境策略](version-policy.md)。

> 文档基线：2026-08-30 当前工作区代码
>
> 适用对象：首次接触本仓库的前端、后端、测试和运维开发人员
>
> 项目定位：使用 Go、React、SQLite 和 Ansible 实现的本地交付编排平台

## 1. 总体结构

仓库采用单仓库全栈结构。主平台由一个 Go 进程提供 REST API、SSE 和构建后的前端静态资源；开发模式下由 Vite 单独提供前端。组件作业最终通过受控的 `ansible-playbook` 子进程在目标环境执行。

文件介质站 FSS 是独立于主平台的 Go 服务，用于保存、校验和分发组件介质。主平台通过环境变量 `FILE_STATION` 定位文件站，通过 `IMAGE_REGISTRY` 定位镜像仓库。

```text
owner/
├── cmd/
│   ├── server/                       # 主平台可执行程序入口
│   ├── backup/                       # 自动备份与恢复 CLI 入口
│   └── fss/                          # 文件介质站可执行程序入口
├── internal/
│   ├── ansible/                      # Ansible 安全执行与日志处理
│   ├── api/                          # HTTP API、会话和 SSE
│   ├── backup/                       # SQLite/Git Catalog 快照、恢复与调度
│   ├── domain/                       # 领域对象、枚举和领域校验
│   ├── fss/                          # 文件介质站服务实现
│   ├── seed/                         # 演示数据初始化
│   ├── sshcheck/                     # 原生 Go SSH 环境检查
│   ├── service/                      # 模块应用服务、发布协调、规划、调度与静态适配端口
│   ├── store/                        # SQLite 首版结构合同与持久化
│   └── ui/                           # React 静态资源服务与嵌入
├── web/
│   ├── e2e/                          # Playwright 端到端测试
│   ├── src/                          # React + TypeScript 前端源码
│   ├── package.json                  # 前端依赖和命令
│   └── pnpm-lock.yaml                # 前端依赖锁文件
├── examples/
│   ├── ansible/                      # 平台允许引用的示例 Playbook 树
│   └── images/                       # 示例镜像构建资源
├── deploy/                           # systemd 和 Docker 部署配置
├── scripts/                          # 测试、部署和离线打包脚本
├── docs/                             # 文档中心、设计、操作、Demo 目录和分类规则
├── data/                             # 本地运行数据，默认不纳入版本控制
├── bin/                              # 本地构建产物，默认不纳入版本控制
├── dist/                             # 离线分发产物
├── Makefile                          # 常用开发、测试和构建入口
├── go.mod                            # Go 模块及依赖
└── README.md                         # 项目简介与快速开始
```

## 2. 运行架构

```text
React / Vite
    │  REST、Cookie、SSE
    ▼
internal/api
    ▼
internal/service
    ├── Catalog / Scenario / Environment / Execution / Read Model
    ├── ReleaseCoordinator ───── 联合发布事务
    ├── internal/store ───────── SQLite
    ├── ActionRunner ──────────── ansible-playbook ── 目标主机
    ├── ImageDelivery ─────────── Docker CLI ──────── 镜像仓库
    └── ArtifactDelivery ──────── HTTP ────────────── FSS 文件介质站
```

核心运行原则：

- API 层负责 HTTP 协议转换，不承载核心业务规则。
- API 层不取得 Store/DB，只调用对应模块应用服务；`Platform` 是静态装配门面。
- Service 层是权限、状态转换、参数合同和调度规则的权威实现。
- Scenario 只维护 Revision/DAG/测试状态，联合发布由 `ReleaseCoordinator` 完成。
- Run 规划、创建、调度、执行、证据记录、回滚规划和审批各有独立对象与文件。
- Store 层负责持久化、事务和并发状态抢占，不决定业务权限。
- Readiness 的一次读取复用一份目录定义；管理页面的历史引用统计单独读取，写入仍在事务内重新校验。
- Domain 层保存跨层共享的数据结构、枚举、通用错误和纯领域校验。
- Run 在创建时锁定组件 Release、场景 Revision、环境 Revision、解析后的结构化参数和 Playbook 摘要。
- 同一环境中的 Run 按 FIFO 串行执行；破坏性动作先等待环境 Owner 审批。
- SSE 只提示前端重新拉取数据，SQLite 中的状态才是最终事实来源。

## 3. Go 后端

### 3.1 `cmd/server`

主平台入口位于 `cmd/server/main.go`，负责：

1. 读取 `.env` 和 `NEWPLATFORM_*` 配置。
2. 打开 SQLite；空库初始化当前结构，已有库必须精确匹配当前合同，否则拒绝启动。
3. 按 Seed Profile 初始化身份或演示数据。
4. 创建 Ansible Runner、Go SSH 环境检查器、Platform Service 和 EventHub。
5. 启动队列恢复、HTTP 服务和优雅退出流程。

开发启动使用 `go run ./cmd/server`；测试环境的单二进制构建使用 `-tags embed` 将前端资源嵌入可执行文件。

环境连通性检查直接使用 Go SSH 客户端：严格读取 `NEWPLATFORM_SSH_KNOWN_HOSTS` 指向的主机指纹文件，并使用 Environment Revision 中显式声明的 SSH CredentialRef 完成认证和 `true` 执行。该检查不调用 Ansible；用户 Catalog Playbook 仍只由 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 管理。

`cmd/backup` 构建独立的 `clusterforge-backup` 自动化与恢复 CLI。它与服务端发布后调度器共用 `internal/backup`，负责 systemd/受保护部署的固定来源快照、校验、续传和只写新路径的恢复；不提供人工 Catalog `snapshot`。独立的 `database` 子命令由 `internal/deploydb` 提供，只负责受保护部署的数据库合同读取、活动 Run 检查、一致性文件备份和完整性校验，不初始化或迁移数据库。环境 Owner 的人工恢复点通过灾备页面调用受权限保护的 HTTP API 创建。

### 3.2 `cmd/fss` 与 `internal/fss`

`cmd/fss/main.go` 启动独立文件介质站，`internal/fss/server.go` 实现具体 HTTP 行为：

- `GET /healthz`：健康检查。
- `PUT /api/v1/files`：上传并校验 SHA-256。
- `POST /api/v1/register`：登记文件站上已有文件。
- `POST /api/v1/fetch`：目标 FSS 从锁定来源 URL 主动获取并校验 SHA-256。
- 普通 GET 路径：下载文件站根目录下的规则文件。
- 写请求按 `CLUSTERFORGE_FSS_WRITE_ALLOW_CIDRS` 限制来源地址。
- 路径解析拒绝绝对路径、目录穿越、符号链接越界和非规则文件。

FSS 使用独立配置前缀 `CLUSTERFORGE_FSS_*`，不直接连接平台 SQLite。

### 3.3 `internal/domain`

领域层集中定义：

- 用户与固定角色。
- Component、ComponentRelease、Readiness、候选交接、依赖、幂等动作、介质与镜像内容身份。
- Scenario、ScenarioRevision、DAG 节点和边。
- Environment、EnvironmentRevision、Inventory、变量和 CredentialRef。
- Run（含整集群 `environment_rollback`）、RunStep、RunLog、Approval、Notification 和 AuditEvent。
- 组件层级/标签、生命周期状态、动作类型、风险等级与通用领域错误。

新增跨层字段时，先更新这里的领域结构，再同步 Store、Service、API 和前端类型。不要仅在 API DTO 或前端中维护另一套业务状态。

### 3.4 `internal/api`

API 使用 Go 标准库 `net/http` 和方法感知的 `ServeMux`。主要文件按资源拆分：

| 文件 | 职责 |
| --- | --- |
| `handler.go` | 路由注册、Cookie 会话、统一响应和错误映射 |
| `workbench.go` | 按角色派生“我的工作”和阻断入口 |
| `components.go` | 组件与 Release 接口 |
| `component_import.go` | 组件批量导入预检与提交 |
| `playbooks.go` | Draft Playbook 上传、读取和编辑 |
| `artifacts.go` | 组件介质与镜像登记、来源修复和移除 |
| `image_builds.go` | 组件镜像构建与查询 |
| `scenarios.go` | 场景、Revision、DAG 校验、测试和运行 |
| `environments.go` | Inventory、Facts、变量和 CredentialRef |
| `platform_parameters.go` | 全局环境参数字段、环境变量字段和参数所有权目录 |
| `environment_transfer.go` | 环境 Revision 导入、导出和差异预检 |
| `catalog_repository.go` | 私有 Catalog 仓库接入、备份和空库恢复 |
| `runs.go` | Run 查询、审批、取消和日志 |
| `events.go` | SSE 事件通道 |
| `notifications.go` | 站内通知和审计查询 |

除身份切换外，`/api/v1/*` 请求都需要 Demo Session Cookie。API 动态响应设置 `Cache-Control: no-store`，避免审批和运行状态被浏览器缓存。

### 3.5 `internal/service`

Service 是业务核心，主要职责包括：

- 校验角色和资源 Owner。
- 管理组件 Release 与场景 Revision 的不可变生命周期。
- 校验组件依赖、公开参数映射和场景 DAG。
- 解析组件固定值、节点 parameterValues、环境参数、上游映射、受治理变量和 CredentialRef。
- 生成组件测试、回滚测试、场景测试和正式运行计划。
- 按环境维护 FIFO Worker，恢复异常中断的 Run。
- 执行审批、取消、通知、审计和日志脱敏。
- 调用 Docker CLI 构建镜像。
- 维护介质/镜像内容身份与可变来源；规划目标优先、来源回退和逐项交付选择，执行时由目标 FSS/Registry 拉取或平移。

新增业务能力时，应优先把可测试的规则写在 Service 层，而不是放进 API Handler 或 React 页面。

### 3.6 `internal/store`

Store 基于 `modernc.org/sqlite`，包含：

- `store.go`：连接、事务辅助和基础查询。
- `component_reads.go`：组件与 Release 列表的只读查询；在同一事务快照中批量加载依赖、Action、介质和镜像，再校验候选审核摘要。
- `catalog_validation.go`：按用途提供选项、目录定义、关系校验快照；定义读取不扫描历史资源引用，最终写入使用事务内校验。
- `run_evidence.go`：通过快照生成列和部分索引读取匹配当前合同、运行时的成功证据，并保持回滚验证门禁语义。
- `release_coordinator.go`：以定义代次、Release 摘要、Scenario 测试证据和全局发布纪元保护独立发布及原子联合发布事务。
- 资源文件：组件、场景、环境、Run、事件、安装记录、镜像构建和介质查询。
- `schema.go`：嵌入首版结构并校验唯一 `schema_contract` 标识。
- `schema.sql`：当前首版的完整数据库结构。

数据库以 `schemaContract` 严格识别结构。当前且唯一接受的合同为 `clusterforge-v1-20260903-run-evidence-indexes`；运行时代码不做历史迁移、模糊兼容、双读或双写。一次性转换工具已移除；其他合同失败关闭，测试库需要变更结构时走显式备份和重建流程。

### 3.7 `internal/ansible`

Ansible 包负责把平台计划安全地转换为外部进程：

- 将 Playbook 限制在 `NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS` 下。
- 为每个 Run 创建隔离工作目录、Inventory 和变量文件。
- 计算并校验 Playbook 树摘要。
- `playbook_validation.go` 为只读目录批量检查文件，合并同一请求中的整棵目录扫描；执行计划与工作区仍独立锁定和验证摘要。
- 解析 CredentialRef，并在子进程运行时注入敏感值。
- 启动和取消完整进程组，处理超时与强制终止。
- 限制日志大小，脱敏后再持久化。
- 解析 Ansible recap，生成结构化执行结果。

对执行安全边界的修改必须同时覆盖单元测试和真实临时 Playbook 集成测试。

### 3.8 `internal/seed`

Seed 以幂等方式创建演示身份、组件、场景和环境：

- `seed.go`：通用身份和目录初始化。
- `kubernetes1175.go`：Kubernetes 1.17.5 组件和场景。
- `openfuyao.go`：保留的 OpenFuyao/BKE 脱敏 Demo 组件、场景和 TEST-NET 环境模板。

`NEWPLATFORM_SEED_PROFILE=demo` 写入上述完整演示数据，包括 OpenFuyao/BKE 脱敏模板和 Kubernetes 样例；`identities` 写入可切换身份，并只在新库创建一次平台治理目录，组件、场景和环境目录仍由人工维护。初始化审计标记持久化后，后续启动不会恢复管理员已删除的条目，也不会覆盖重命名或停用状态；已有目录第一次运行新版时只登记标记。`docs/demo-catalog.md` 记录的是测试平台当前业务数据，不等同于内置 Demo Seed。

### 3.9 `internal/ui`

UI 包负责提供 React 静态文件：

- 无 `embed` 构建标签时，从 `web/dist` 读取文件。
- 使用 `-tags embed` 时，从 `internal/ui/dist` 嵌入资源。
- React 客户端路由找不到真实文件时回退到 `index.html`。
- 哈希静态资源长期缓存；`index.html` 和 `version.json` 禁止缓存。

`internal/ui/dist` 是构建中间产物，不应手工编辑。

## 4. React 前端

前端位于 `web/`，使用 React 18、TypeScript、Vite、React Router 和 XYFlow。

```text
web/src/
├── api/client.ts                    # fetch 封装、首版 DTO 映射和错误处理
├── components/                      # 应用外壳、版本守卫和公共编辑器
├── context/AppContext.tsx           # 当前用户、身份切换和全局刷新
├── hooks/useApiData.ts              # 按查询范围隔离数据、取消请求和合并刷新
├── pages/                           # 按产品导航拆分的页面
├── test/                            # Vitest 与 Testing Library 测试
├── types/                           # 前端领域类型和约束辅助
├── App.tsx                          # 页面路由
├── main.tsx                         # React 应用入口
└── styles.css                       # 当前全局样式
```

主要页面：

| 路由 | 页面 | 主要用途 |
| --- | --- | --- |
| `/` | `DashboardPage` | 可见资产和运行概览 |
| `/components` | `ComponentsPage` | 组件、Release、Playbook、介质和镜像 |
| `/scenarios` | `ScenariosPage` | DAG 编排、校验、测试和发布 |
| `/environments` | `EnvironmentsPage` | Inventory、Facts、变量和凭据引用 |
| `/disaster-recovery` | `DisasterRecoveryPage` | Catalog 仓库接入、备份状态和空库恢复 |
| `/runs` | `RunsPage` | 队列、审批、步骤和实时日志 |
| `/notifications` | `NotificationsPage` | 发布影响通知 |
| `/manual` | `OperationManualPage` | 内置操作手册 |
| `/manual/:section` | `OperationManualPage` | 直接打开指定手册章节 |

浏览器写操作前应以 API 返回状态为准。前端按钮隐藏只改善交互，不构成权限边界。

## 5. Playbook 与示例资源

`examples/ansible` 既是样例目录，也是默认允许执行的 Playbook 根目录：

| 目录 | 内容 |
| --- | --- |
| `k8s-1.17.5-cluster` | 细粒度 Kubernetes 1.17.5 组件、验证入口和来源 Role |
| `k8s-1.17.5-kubeadm` | 较小的 kubeadm 示例 |
| `openfuyao` | OpenFuyao/BKE 作业的脱敏快照、适配合同和只读预检样例；不代表当前测试目录或真实环境验收 |
| `managed` | 前台上传或在线编辑的 Draft Playbook，运行时按配置产生 |

组件 Action 保存相对于允许根目录的 Playbook 路径。Released Release 不可修改；Draft Playbook 内容变化会使已有测试验证失效。

## 6. 部署、脚本与生成物

### 6.1 `deploy`

- `deploy/platform/`：主平台 systemd Unit 和环境配置示例。
- `deploy/fss/`：文件介质站 systemd Unit。
- `deploy/docker/`：镜像构建所需的 Docker daemon 配置示例。

### 6.2 `scripts`

| 脚本 | 用途 |
| --- | --- |
| `deploy-test-88-55.sh` | 构建并部署主平台测试环境，包含备份、就绪检查和失败回退 |
| `deploy-fss-88-57.sh` | 构建并部署文件介质站 |
| `test-k8s1175-components.sh` | Kubernetes 1.17.5 组件作业门禁 |
| `test-openfuyao-components.sh` | OpenFuyao/BKE 脱敏快照的语法、任务清单和失败关闭合同门禁 |
| `pack-usb.sh` | 生成离线 USB 分发包 |

部署脚本中的主机地址和路径属于具体环境配置，复用前必须重新确认目标、活动 Run、备份位置和服务状态。

### 6.3 生成目录

- `data/`：默认 SQLite 数据库、WAL、Run 工作区和镜像构建临时目录。
- `bin/`：本机构建的可执行文件。
- `web/dist/`：Vite 构建输出。
- `internal/ui/dist/`：准备嵌入 Go 二进制的前端输出。
- `dist/`：离线分发包；当前仓库可能保存经过确认的发布快照。

除明确更新分发包外，不要把运行数据库、临时工作区或本机构建产物当作源码修改。

## 7. 常用开发命令

```bash
# 安装依赖
make bootstrap

# 同时启动 Go API 和 Vite
make dev

# 只启动后端或前端
make dev-api
make dev-web

# 初始化或重建 Demo 数据
make seed
make reset-demo

# Go、React 和 Ansible 门禁
make test

# Playwright 端到端测试
make test-e2e

# 构建嵌入前端的主平台二进制
make build
```

`make test` 中的真实 Ansible 测试依赖本机存在 `ansible-playbook`。缺少运行依赖时应将该门禁报告为“未验证”，不能视为通过。

## 8. 开发定位指南

| 需求 | 通常需要检查或修改的位置 |
| --- | --- |
| 新增领域字段 | `internal/domain` → `internal/store`/首版结构 → `internal/service` → `internal/api` → `web/src/types` 和页面 |
| 新增 API | `internal/api` 路由与 Handler，同时在 Service 层实现规则并补 API 测试 |
| 修改权限或状态机 | `internal/service`，必要时同步 Domain；前端只展示结果 |
| 修改数据库结构 | 更新 `internal/store/schema.sql` 与 `schemaContract`；需要保留数据时使用单独受控转换流程，完成后不把历史迁移留在运行时代码中 |
| 修改 Run 规划或调度 | `plan_builder.go`、`run_creator.go`、`run_scheduler.go`、`run_executor.go`、`lifecycle_recorder.go`、`rollback_planner.go`、`approval_service.go` 及相关测试 |
| 修改 Ansible 安全行为 | `internal/ansible`，同时补单元和集成测试 |
| 新增组件示例 | `examples/ansible`、`internal/seed` 及 Seed/组件脚本测试 |
| 新增前端页面 | `web/src/pages`、`App.tsx`、API Client、类型和测试 |
| 修改介质交付或文件站 | `delivery_binding.go`、`delivery_planner.go`、`delivery_adapters.go`、`internal/store/artifacts.go`、`internal/fss`、运行审批页和组件页 |
| 修改镜像身份、交付或构建 | `internal/service/images.go`、`image_builds.go`、Delivery 适配器、Store/API 和组件页 |
| 修改测试部署 | `deploy` 与 `scripts`，并执行实际测试环境验证 |

## 9. 修改前后的检查原则

1. 先运行 `git status --short --branch`，保留不属于本次任务的工作区改动。
2. 以当前代码和首版结构合同为准；文档可能落后，发现差异时同步更新相关文档。
3. Released Release 和 Released Scenario Revision 不可原地修改，应克隆新 Draft。
4. Secret 只能通过 CredentialRef 进入运行时，不能写入普通参数、日志、Seed 或 Run 快照。
5. 分开报告代码测试、Ansible 门禁、构建、部署和真实环境验收，它们不能互相替代。
6. 对数据库、Run 调度、审批、回滚、介质平移和部署脚本的修改应优先采用失败关闭策略。

文档导航和维护职责参见 [文档中心](README.md)；更详细的业务模型和运行规则参见 [平台设计文档](backend-design.md)，面向平台使用者的操作流程参见 [平台操作手册](operation-manual.md)。

## 10. 列表读取与刷新性能

组件与 Release 的列表装配集中在 `component_reads.go`，创建和更新仍在 `components.go`。列表先读取组件与 Release，再以每批最多 256 个 Release ID 分别读取四张子表，复用详情查询的行解码逻辑。读取游标关闭后才执行下一条 SQL，因此单连接池也能完成查询。整个列表使用一个只读事务快照，避免并发编辑时混合新旧合同；候选可见性仍在子表装配完整后通过 `IsApprovedCandidate()` 校验。

对于有可见 Release 的目录，列表 SELECT 数量从 `1 + 组件数 + 4 × Release 数` 降为 `2 + 4 × ceil(Release 数 / 256)`，不含事务控制语句。无组件时只有一次 SELECT；有组件但无可加载 Release 时为两次。

前端 `useApiData` 在同一查询范围内保留当前请求，将请求期间到达的多个刷新信号合并为一次补拉。每次请求完成后即可显示结果，持续日志事件不会反复取消慢请求。身份或查询参数变化、手动重试仍立即取消并替换请求；卸载时取消请求，旧范围的响应不能写入新范围。运行详情页复用这一逻辑，并将用户 ID 和 Run ID 都作为查询范围。

### 本机基准记录（2026-09-03）

Apple M2、darwin/arm64，使用临时内存 SQLite 和合成目录，每个组件包含 3 个 Draft Release、每个 Release 包含 1 个 Action。以下为同一基准连续三次运行的中位数，仅测量 Store 列表读取，未包含 Service 的 Readiness 计算、HTTP 序列化或页面渲染。

| 目录规模 | 优化前耗时 | 优化后耗时 | 耗时减少 | 优化前 / 后分配次数 |
| --- | --- | --- | --- | --- |
| 10 个组件 / 30 个 Release | 1.940 ms | 0.607 ms | 69% | 7,330 / 4,464 |
| 40 个组件 / 120 个 Release | 8.371 ms | 1.770 ms | 79% | 29,231 / 17,138 |

复现命令：

```bash
go test ./internal/store -run '^$' -bench '^BenchmarkListComponents$' -benchmem -count=3
```

回归覆盖跨批次完整合同、排序、单连接池、并发编辑时的快照一致性、候选审核摘要失效，以及前端连续刷新、切换查询范围、失败后补拉和卸载取消。测试只使用合成数据，不读取运行中的数据库或目标环境。

### Readiness 目录定义与完整 HTTP 处理基准

`readiness_evaluation.go` 管理一次列表或详情读取的目录定义复用。校验只需要选项、参数和变量定义，使用 `ReadCatalogDefinitions` 在四次 SELECT 中读取；单独读取选项使用 `ReadCatalogOptions`，只需要两次 SELECT。分类与选项分别批量读取，参数和变量的定义查询不计算历史引用次数。

管理页面仍使用带引用统计的查询，并在一个事务快照内读取统计与定义。删除选项、分类或参数时仍在写事务中检查真实引用。Readiness 只在当前读取操作内复用定义，不保存进程级缓存；各根 Release 保留独立的依赖遍历状态，每次计算仍查询 Run 证据并匹配当前合同摘要。新请求读取最新定义，预览中的定义不会带入后续写入校验。

2026-09-03 在上述 Store 优化基础上，使用同一 Apple M2、内存 SQLite，测量 `GET /api/v1/components` 的完整服务端处理：鉴权、列表读取、Readiness、DTO 装配及 JSON 序列化。每个组件包含 3 个 Draft，每个 Draft 包含 Install、Verify、Rollback 三个 Action；没有成功 Run，因此每个 Release 必须返回安装和回滚证据缺失两个阻断项。基准开始前会校验组件、Release 和阻断项数量，避免遗漏功能带来的虚假提速。

下表为连续三次运行的中位数，优化前指已完成上一节 Store 批量读取、尚未拆分目录定义与引用统计时的状态。单次分配字节表示累计分配量，不是常驻内存。

| 目录规模 | 本轮优化前 | 本轮优化后 | 耗时减少 | 单次分配字节：前 / 后 |
| --- | --- | --- | --- | --- |
| 10 个组件 / 30 个 Release | 25.837 ms | 2.534 ms | 90% | 9.68 MB / 0.84 MB |
| 40 个组件 / 120 个 Release | 245.280 ms | 9.018 ms | 96% | 125.09 MB / 3.42 MB |

```bash
go test ./internal/api -run '^$' -bench '^BenchmarkComponentsHTTP$' -benchmem -count=3
```

回归检查定义读取不查询历史表、管理页面引用数与删除保护保持有效、同一次读取只加载一份定义、新请求看到目录更新、目录读取失败仍阻断，以及运行时证据新增和合同摘要变化即时生效。

以上 HTTP 基准未包含网络传输、真实 Playbook 文件校验和浏览器渲染。列表响应仍包含完整 Release，Run 证据仍逐项查询；继续扩容时应测量真实请求分布，再评估列表摘要、分页或证据批量读取。

### Run 证据数据结构与历史查询基准

Run 快照保留为 JSON，在 `runs` 上为摘要、证据类型、运行时、运行时版本和证据时间增加五个虚拟生成列。应用只写原始字段，SQLite 自动维护生成列及索引，避免额外维护一份证据状态。成功组件测试按 Release、当前摘要、证据类型建立部分索引；指定运行时的查询使用另一个包含运行时及版本的索引。回滚验证分别读取两种合格证据的最新一条，最终只比较最多两条候选。另为活跃组件测试、Action 的 Release 外键和 RunStep 的 Run 外键补充索引。

2026-09-03，Apple M2、darwin/arm64、临时内存 SQLite，测量一次指定运行时的安装与回滚证据查询。所有历史 Run 属于同一 Release；只有最新两条匹配当前摘要，其余匹配旧摘要。以下是连续三次运行的中位数，优化前为前两节优化完成、尚未增加生成列和索引时的状态。

| 历史 Run 数 | 本轮优化前 | 本轮优化后 | 耗时减少 |
| --- | --- | --- | --- |
| 1,000 | 0.861 ms | 0.0665 ms | 92% |
| 10,000 | 7.624 ms | 0.0668 ms | 99% |

```bash
go test ./internal/store -run '^$' -bench '^BenchmarkComponentEvidenceHistory$' -benchmem -count=3
```

这组基准只测证据定位，不包含完整 HTTP 请求、真实数据库磁盘读取或写入。新查询的单次分配从约 1.89 KB 增至 4.71 KB，索引还会增加数据库空间及写入维护成本；收益是避免逐行解析历史快照与大范围排序。执行计划回归确认使用指定索引，行为回归覆盖摘要/运行时匹配、状态转换、元数据缺失或类型错误、交付结果更新，以及 JSON 来源与生成列一致性。部署脚本使用随备份 CLI 编译的 Go SQLite 引擎，并通过临时数据库检查文件备份保留生成值和索引、纳入已提交 WAL、拒绝外键损坏且不覆盖既有文件。

这次结构变更使用 `clusterforge-v1-20260903-run-evidence-indexes` 合同，旧合同数据库会被拒绝打开。上述基准与回归使用合成数据和临时库；测试环境的数据迁移与部署验收单独执行、单独记录。

### 非数据库优化：只读 Playbook 检查与工作台装配

组件列表和详情中的 Readiness 只需要确认 Playbook 路径、文件和执行目录可读。原先每个 Action 调用一次 `Digest`，重复读取同一目录的全部文件。现在 `Runner.ValidatePlaybooks` 为每个不同路径保留独立检查结果，在一批可见 Action 中只计算一次共享目录摘要。缺失文件、路径越界和符号链接越界仍只阻断对应 Action；共享目录读取失败会阻断所有依赖该目录的有效路径。列表未覆盖的候选依赖文件继续独立实时校验。

批量检查结果只属于一次同步列表或详情读取，不存入 Runner、Platform 或数据库。下一次请求重新检查；发布验证、计划摘要和执行工作区校验继续独立运行，不复用只读结果。组件 Owner 工作台直接使用同一次 `ListComponents` 已计算的 Readiness，以纯函数装配工作项，避免再次读取文件和证据；没有有效 Readiness 的输入会返回错误。

2026-09-03，Apple M2、darwin/arm64，在已完成上述数据库优化的代码上测量完整组件 HTTP 处理。临时目录包含三个共享 Playbook 和 64 个各 16 KiB 的资源文件（约 1 MiB）；每个组件三个 Draft、每个 Draft 三个 Action，均引用这三个文件。临时内存 SQLite 没有成功 Run，响应必须保留两个证据阻断项。以下为三次运行的中位数，每次测量三个请求：

| 目录规模 | 优化前 | 优化后 | 耗时减少 | 单次累计分配：前 / 后 |
| --- | --- | --- | --- | --- |
| 10 个组件 / 30 个 Release | 155.691 ms | 4.886 ms | 97% | 210.72 MB / 3.43 MB |
| 40 个组件 / 120 个 Release | 618.805 ms | 12.729 ms | 98% | 842.79 MB / 6.74 MB |

```bash
go test ./internal/api -run '^$' -bench '^BenchmarkComponentsHTTPWithPlaybooks$' -benchtime=3x -benchmem -count=3
```

这组基准包含真实临时文件读取、鉴权、Readiness 和响应序列化，不包含网络、浏览器渲染或 Ansible 子进程执行；操作系统可能缓存文件。独立 Playbook 路径更多时仍需逐个读取文件，因此不能直接将该比例视为测试环境端到端收益。累计分配量也不等于常驻内存。

回归覆盖批量与逐项检查的完整响应一致性、整棵目录仅扫描一次、不同文件错误归属、下一请求看到文件删除、未覆盖路径实时回退，以及发布验证不继承只读缓存。工作台回归确认保留阻断项且不重复校验。本轮不变更数据库结构、SQL 或 API 合同。
