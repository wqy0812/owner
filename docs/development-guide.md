# 开发与文档维护指南

> 基线：2026-09-07 当前工作区代码。描述当前 V1；已实现、已测试、已部署、已在真实环境验收需要分别记录。

## 先用文档定位任务

第一次接触平台，按[功能与核心概念](platform-capabilities.md) → [项目结构](project-structure.md) → [后端合同](backend-design.md)阅读。具体按钮和角色路径查[平台说明书](platform-manual.md)，不需要先通读全部历史方案。

| 要开展的工作 | 先读什么 | 代码入口 | 验证入口 |
| --- | --- | --- | --- |
| 页面、表单、导航 | 平台说明书、项目结构第 4 节 | `web/src/App.tsx`、`components/AppShell.tsx`、对应 `pages/`、`api/client.ts`、`types/domain.ts` | `web/src/test/`、`web/e2e/critical-flows.spec.ts`、桌面浏览器 |
| 参数权限、公开引用和环境填写 | 后端设计第 4、7.2 节 | `internal/domain/component_parameters.go`、`internal/service/parameters.go`、`parameter_graph.go`、`environment_parameters.go` | `parameter_ownership_test.go`、`parameter_graph_test.go`、前端 `ParameterEditors.test.tsx` |
| 适配标签 | 功能说明、运行历史与适配专项 | `internal/domain/adaptation.go`、`internal/service/scenarios.go`、`internal/store/scenario_adaptation.go`、`web/src/types/adaptation.ts` | Domain/Service 适配测试、前端 `adaptation.test.ts` |
| 发布线、审核、候选与联合发布 | 后端设计第 4、6、7.1 节 | `internal/service/release_drafts.go`、`readiness.go`、`release_coordinator.go`、`internal/store/release_coordinator.go` | Release Draft、Readiness、Coordinator 测试与 `internal/api/api_test.go` |
| Playbook 工作区 | 平台说明书第 3 节、后端设计第 8 节 | `internal/service/playbooks.go`、`playbook_workspace.go`、`internal/store/playbook_files.go`、`internal/ansible/` | `playbook_workspace_test.go`、Runner 测试与 `make test-ansible` |
| 场景依赖连线和参数来源 | 后端设计第 7 节 | `internal/service/scenario_graph.go`、`parameter_graph.go`、`plan_builder.go`、`web/src/pages/scenarioGraph.ts` | `scenario_graph_test.go`、`parameter_graph_test.go`、前端 `scenarioGraph.test.ts` |
| Inventory 与 Revision | 平台说明书第 5 节 | `internal/service/environments.go`、`internal/api/environment_transfer.go`、`web/src/components/EnvironmentInventoryEditor.tsx` | Environment/导入权限测试、前端 `EnvironmentInventoryEditor.test.tsx` |
| Run 计划、审批、取消与续跑 | 后端设计第 7 节 | `internal/service/plan_builder.go`、`run_creator.go`、`run_executor.go`、`approval_service.go`、`run_retry.go` | Run/审批/重试测试；真实执行另按授权验收 |
| 组件直接引用、列表性能 | 后端设计第 9.6 节、项目结构第 10 节 | `internal/service/component_usage.go`、`component_read_context.go`、`internal/store/component_summaries.go`、`run_summaries.go` | `internal/api/read_models_test.go`、`history_usage_test.go`、对应基准 |
| Run 归档、清理与恢复 | [运行历史管理](run-history-and-adaptation.md)、[备份恢复](catalog-backup-and-restore.md) | `internal/service/run_archive.go`、`internal/store/run_archive.go`、`run_cleanup.go`、`internal/runarchive/`、`internal/backup/run_history.go` | 对应归档、并发保护、备份恢复测试及临时文件验证 |
| 平台目录、工作台和通知 | 后端设计第 6、9、10 节 | `internal/service/platform_options.go`、`platform_parameters.go`、`workbench.go`、前端 `PlatformManagementPage.tsx` | 目录引用/RBAC 测试、前端 `App.test.tsx` |
| 部署与数据库合同 | 根 README、版本策略、备份恢复 | `scripts/deploy-test-88-55.sh`、`internal/store/schema.go`、`schema.sql`、`internal/deploydb/` | `make test-deploy-script`、Store/备份测试；部署后独立验收 |

表中仅省略同一单元格内重复的目录前缀。例如 `parameter_graph.go` 位于紧邻的 `internal/service/` 下。最终路由注册以 `internal/api/handler.go` 为准，不能仅通过前端按钮推断服务端能力。

## 启动一个隔离的开发实例

先执行 `git status --short --branch`，保留现有未提交工作；确认端口和目标数据库。不要为了启动页面而重建正在使用的业务库。依赖条件与安装命令见根 README；以下命令使用新的临时数据库，仅初始化身份及治理目录。

在仓库根目录的第一个终端执行：

```bash
task_data_dir=$(mktemp -d /tmp/clusterforge-dev.XXXXXX)
mkdir -p "$task_data_dir/playbooks"
NEWPLATFORM_ADDR=127.0.0.1:18080 \
NEWPLATFORM_DB_PATH="$task_data_dir/platform.db" \
NEWPLATFORM_RUN_ROOT="$task_data_dir/runs" \
NEWPLATFORM_IMAGE_BUILD_ROOT="$task_data_dir/image-builds" \
NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS="$task_data_dir/playbooks" \
NEWPLATFORM_SEED_PROFILE=identities \
CLUSTERFORGE_BACKUP_ENABLED=false \
CLUSTERFORGE_RUN_ARCHIVE_DIR="$task_data_dir/archives" \
go run ./cmd/server
```

在第二个终端从仓库根目录执行：

```bash
VITE_PROXY_TARGET=http://127.0.0.1:18080 \
pnpm --dir web dev --host 127.0.0.1 --port 15173 --strictPort
```

访问 `http://127.0.0.1:15173`。关闭两个终端进程即停止该实例。端口占用时同时调整后端地址和 Vite 代理。临时目录为空，不包含真实主机；需要验收数据时使用合成数据或本地 Demo，不复制真实凭据。`identities` 仅是 Seed 选择，不会禁用执行器；发起真实 Run 仍必须确认目标和授权。

## 跨层修改顺序与不变量

1. 写清触发条件、原有结果、预期结果、涉及角色及版本状态；先判断是缺陷、合同变更还是显示调整。
2. 沿 Domain → Store → Service → API → Client/前端类型 → 页面查找读写链；业务规则在 Service，事务一致性在 Store，前端隐藏不是权限控制。
3. 新增字段同步请求、响应、持久化、摘要、导入导出、Catalog 与测试夹具；影响执行的字段必须检查旧审核、测试证据和计划失效。
4. 保持已发布 Release、场景 Revision、环境历史 Revision 和 Run 快照不可变。依赖锁定精确 Release，手工顺序边不能代替依赖，Secret 只走 CredentialRef。
5. 变更数据库时同步完整 `schema.sql` 和唯一 `schemaContract`。V1 运行时不提供历史迁移或旧字段兼容；普通启动应拒绝不匹配合同。重建有数据的测试库另需明确授权和备份；限定的 `foundation-snapshot` 只向新库转换账号和基础目录，不恢复旧业务或 Run。
6. 根据实际改动范围选择验证，再更新下表文档。提交、推送、部署和真实验收分别说明，不能从本地绿灯推断环境已更新。

## 验证怎么选

以下均从仓库根目录执行。测试数据放在临时目录，不能将测试夹具注册为真实组件或场景。

| 变更范围 | 检查命令和证据 |
| --- | --- |
| 文档 | `make check-docs`、`git diff --check`；人工核对功能语义 |
| 前端逻辑、类型、布局或共享功能说明 | `pnpm --dir web test`、`pnpm --dir web build`；桌面浏览器验证导航、表单、弹窗和滚动 |
| Go 业务或 API | 先运行目标包/用例，跨模块后运行 `go test ./...` 与 `go vet ./...` |
| 发布、调度、归档、引用并发 | 针对变更运行相关 `go test -race`；验证并发漂移和失败关闭行为 |
| Playbook/Runner | `make test-ansible`；缺少 Ansible 时写明未验证 |
| 部署脚本 | `make test-deploy-script`，实际部署必须另外授权和验证 |
| 浏览器端到端 | `make test-e2e`；连接隔离本地 API 的浏览器测试使用 `make test-e2e-live`，用例见 `web/e2e/` |

桌面布局至少检查 1280×800、1440×900 与 1920×1080 CSS 像素：导航可达、侧栏可收起、表格可滚动、DAG 和检查器可用、弹窗按钮可见。不需要移动端断点、底部导航或触屏专用页面。

`make test` 包含部署脚本、夹具边界、Go、前端与 Ansible 门禁；`make build` 还会更新嵌入式 UI 产物。根据变更选择门禁，缺少依赖、失败、未执行要分别记录。

## 文档维护约定

| 事实类型 | 内容源与更新方式 |
| --- | --- |
| 平台功能和对象关系 | `platform-capabilities.md`，前端直接导入；只使用现有渲染器支持的二/三级标题、段落、列表、引用、粗体和行内代码 |
| 页面操作、条件和结果 | `platform-manual.md` 与 `web/src/pages/PlatformManualPage.tsx` 的角色/公共按钮说明同步修改 |
| API、数据库、权限和状态机 | `backend-design.md`；REST 表用真实方法和完整路径，不使用可选路径简写 |
| 代码入口、配置和启动 | `project-structure.md`、本指南和根 README |
| 运维专项 | `catalog-backup-and-restore.md`、`run-history-and-adaptation.md`；清楚区分 Catalog 和完整历史恢复 |
| 环境事实、性能、验收 | 带日期的快照或 `records/`，注明代码版本、数据来源、执行范围和未验证项；不能把旧记录描述成当前事实 |
| 已完成方案或讨论 | `history/`，不作为当前需求清单；尚未实现的建议明确标注，不混入功能说明 |

`make check-docs` 离线校验 Markdown 相对文件链接、API 方法/路径覆盖、正式文档中的数据库合同号、页面路由覆盖和 API 文件索引。它不能证明说明的业务语义正确，也不会联网验证外链、部署状态或执行真实 Run。

每次交接至少留下：问题与最终行为、代码入口、文档变更、已执行验证及结果、剩余限制。尚未完成的工作注明下一步、阻断原因和验收条件；历史验收数量、主机地址和旧基准只能作为定位线索，开展新任务时重新核对。
