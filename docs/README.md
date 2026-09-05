# ClusterForge 文档中心

> 代码文档基线：2026-09-05 当前工作区；不代表测试环境已部署。资产目录与节点文件保留各自日期的历史快照
> 版本与环境：当前仅有首个版本（V1），所有部署与验收对象均为测试环境。统一口径见[首版与环境策略](version-policy.md)。

本目录集中保存当前功能与开发说明，并通过索引区分带日期的快照、专项验收和历史方案。代码、数据库合同或前台入口变化时，应直接更新对应正式文档；`history/` 单独归档已经完成或不具约束力的历史方案与讨论，不作为当前合同。

## 阅读入口

| 读者或目的 | 首选文档 | 内容边界 |
| --- | --- | --- |
| 开展开发或交接任务 | [开发与文档维护指南](development-guide.md) | 按任务定位代码和测试、隔离启动、验证选择与维护规则 |
| 理解平台功能 | [平台功能与核心概念](platform-capabilities.md) | 功能、对象关系和职责；与前端功能章节共用内容源 |
| 第一次接触仓库 | [项目结构说明](project-structure.md) | 目录、运行架构、开发入口与常用命令 |
| 后端开发、测试与设计 | [平台设计文档](backend-design.md) | 领域模型、权限、状态机、数据结构、API 与安全边界 |
| 平台操作者 | [平台说明书](platform-manual.md) | 功能入口、工作流、角色操作条件、结果与风险 |
| 适配范围、直接引用与历史管理 | [运行历史与适配说明](run-history-and-adaptation.md) | 当前匹配规则、成功归档、失败清理、完整历史备份 |
| 灾备配置、备份与恢复 | [Catalog 与数据库备份恢复](catalog-backup-and-restore.md) | 私有 Git Catalog、SQLite 快照、恢复点、空库恢复和失败边界 |
| 复现带日期的测试目录 | [测试环境资产目录](demo-catalog.md) | 2026-08-30 身份、资产、恢复点与复现步骤；使用前重新核对 |
| 设计或维护组件分类 | [组件分类规则](组件分类规则.md) | L1-L6、分类字段及当前组件映射 |
| 回看 Kubernetes 双版本录入方案 | [Kubernetes 1.17.5 / 1.34.3 双版本组件方案](kubernetes-dual-version-component-plan.md) | 2026-08-28 录入时的版本与依赖方案，非当前发布合同 |
| 查阅测试节点快照 | [2026-08-30 测试环境节点快照](deployment-snapshot-2026-08-30.md) | 2026-08-30 Registry、Inventory、Revision 与连通性证据 |
| 查阅原始节点历史 | [2026-08-20 测试环境节点快照](deployment-snapshot-2026-08-20.md) | 2026-08-20 的静态节点记录，不代表实时状态 |
| 判断版本和兼容策略 | [首版与环境策略](version-policy.md) | V1 数据库、API 和测试环境解释规则 |
| 重置或初始化测试环境 | [Kubernetes 重置与 Bootstrap 标准](kubernetes-reset-bootstrap-standard.md) | UI 优先、审批、清理顺序、恢复与验收边界 |
| 查阅列表优化测量 | [2026-09-03 性能记录](records/2026-09-03-read-performance.md) | 旧实现及测量，不代表当前读取合同 |
| 查阅本轮分批审核 | [2026-09-05 代码审核](records/2026-09-05-code-review.md) | 提交批次、已修复问题、用户决定与本地验证边界 |
| 查阅三方案验收 | [2026-09-05 三方案验收](verification-three-plans-2026-09-05.md) | 适配、直接引用、归档的本地验收与边界 |
| 查阅历史验收证据 | [2026-08-24 Kubernetes 细粒度验收](records/2026-08-24-k8s-1.17.5-ubuntu-fine-grained-acceptance.md) | `docs/records/` 下按日期保存的静态验收记录，不代表实时状态 |
| 查阅双版本组件录入证据 | [2026-08-28 双版本组件前台录入](records/2026-08-28-kubernetes-dual-version-component-entry.md) | 前台路径、计划指纹、生成 ID、审计时间和最终计数 |
| 回看模块化实施决策 | [ClusterForge 模块化与简化实施方案](history/ClusterForge%20模块化与简化实施方案.md) | 已完成方案的原始决策依据和实施顺序，不作为当前事项清单 |
| 回看非约束架构讨论 | [架构师探讨纪要](history/架构师探讨纪要（不作为约束）.md) | 架构讨论中的问题与选择，不作为需求或设计合同 |

仓库根目录的 [README](../README.md) 只承担项目简介、快速启动、测试和部署入口，不重复完整设计或操作步骤。

## 文档职责

- `backend-design.md` 是后端合同的权威说明；API、数据库、权限、Planner 或安全规则变化时更新。
- `development-guide.md` 是后续工作的任务入口；新增模块、命令或交接规则时同步维护。
- `platform-capabilities.md` 是用户功能说明的内容源，前端直接导入，修改后重新构建。
- `platform-manual.md` 是平台说明书，关联功能章节并说明用户操作；页面按钮、出现条件、交互结果或风险边界变化时更新。
- `catalog-backup-and-restore.md` 是灾备运维合同；备份调度、Catalog 分支/标签、恢复预检或 CLI 行为变化时更新。
- `demo-catalog.md` 是带日期的测试环境发布目录和配置快照；目录发布、恢复点或环境 Revision 变化时，应重新核对并刷新基线时间。
- `组件分类规则.md` 同时保存通用分类原则和带日期的测试目录映射；分类枚举或已发布组件变化时更新。
- `kubernetes-reset-bootstrap-standard.md` 定义测试环境 reset/bootstrap 的标准操作与失败关闭边界，不替代某次执行记录。
- `records/` 只保存带日期的历史验收证据；后续状态变化必须新增或链接新记录，不能回写为“当前状态”。
- `history/` 只保存已经完成或不具约束力的历史方案与讨论；当前行为仍以代码、数据库合同和正式文档为准。
- 带日期的部署快照是历史证据，禁止覆盖成“当前实时状态”；需要新快照时新增日期文件或显式更新日期和验证证据。

## 维护检查

修改代码后至少检查下表中的文档：

| 代码变化 | 必查文档 |
| --- | --- |
| API、DTO、数据库结构、权限、状态机 | `backend-design.md` |
| 前台页面、按钮、表单和流程 | `platform-capabilities.md`、`platform-manual.md`、`PlatformManualPage.tsx` |
| 模块入口、开发工作流和验证命令 | `project-structure.md`、`development-guide.md` |
| 适配、引用、归档和清理 | `run-history-and-adaptation.md`、`backend-design.md`、平台说明书 |
| 备份调度、Catalog 仓库/恢复点、恢复预检或灾备 CLI | `catalog-backup-and-restore.md`、`backend-design.md`、`project-structure.md` |
| 测试目录中的组件、Release、场景、环境或恢复点 | `demo-catalog.md`、`组件分类规则.md` |
| 启动、测试、构建或部署脚本 | 根 `README.md`、`project-structure.md` |
| 版本或兼容策略 | `version-policy.md` 及所有文档顶部版本声明 |

合并前应执行 `make check-docs`（需要 Python 3）和 `git diff --check`，并把代码测试、构建、部署和真实环境验收分开报告。
`backend-design.md` 的 REST API 表必须覆盖 `internal/api/handler.go` 注册的全部 `/api/v1` 路由；新增路由时不能只更新前台说明或专项文档。
