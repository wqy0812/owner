# ClusterForge 文档中心

> 文档基线：2026-08-25 当前工作区
> 版本与环境：当前仅有首个版本（V1），所有部署与验收对象均为测试环境。统一口径见[首版与环境策略](version-policy.md)。

本目录只保存仍然有效的说明。代码、数据库合同或前台入口变化时，应直接更新对应正式文档，不保留“旧文档如何解释”的项目记忆文件。

## 阅读入口

| 读者或目的 | 首选文档 | 内容边界 |
| --- | --- | --- |
| 第一次接触仓库 | [项目结构说明](project-structure.md) | 目录、运行架构、开发入口与常用命令 |
| 后端开发、测试与设计 | [平台设计文档](backend-design.md) | 领域模型、权限、状态机、数据结构、API 与安全边界 |
| 平台操作者 | [平台操作手册](operation-manual.md) | 按角色说明页面入口、操作条件、结果与风险 |
| 查看内置样例资产 | [Demo 资产目录](demo-catalog.md) | Seed 身份、组件、场景、环境和已知样例边界 |
| 设计或维护组件分类 | [组件分类规则](组件分类规则.md) | L1-L6、分类字段及当前组件映射 |
| 了解测试环境节点 | [测试环境节点快照](deployment-snapshot-2026-08-20.md) | 2026-08-20 的静态节点记录，不代表实时状态 |
| 判断版本和兼容策略 | [首版与环境策略](version-policy.md) | V1 数据库、API 和测试环境解释规则 |
| 重置或初始化测试环境 | [Kubernetes 重置与 Bootstrap 标准](kubernetes-reset-bootstrap-standard.md) | UI 优先、审批、清理顺序、恢复与验收边界 |
| 查阅历史验收证据 | [2026-08-24 Kubernetes 细粒度验收](records/2026-08-24-k8s-1.17.5-ubuntu-fine-grained-acceptance.md) | `docs/records/` 下按日期保存的静态验收记录，不代表实时状态 |

仓库根目录的 [README](../README.md) 只承担项目简介、快速启动、测试和部署入口，不重复完整设计或操作步骤。

## 文档职责

- `backend-design.md` 是后端合同的权威说明；API、数据库、权限、Planner 或安全规则变化时更新。
- `operation-manual.md` 是用户操作的权威说明；页面按钮、出现条件、交互结果或风险边界变化时更新。
- `demo-catalog.md` 只描述 `NEWPLATFORM_SEED_PROFILE=demo` 生成的样例资产，不把真实环境数据写入文档。
- `组件分类规则.md` 同时保存通用分类原则和当前 Seed 映射；分类枚举或 Seed 组件变化时更新。
- `kubernetes-reset-bootstrap-standard.md` 定义测试环境 reset/bootstrap 的标准操作与失败关闭边界，不替代某次执行记录。
- `records/` 只保存带日期的历史验收证据；后续状态变化必须新增或链接新记录，不能回写为“当前状态”。
- 带日期的部署快照是历史证据，禁止覆盖成“当前实时状态”；需要新快照时新增日期文件或显式更新日期和验证证据。

## 维护检查

修改代码后至少检查下表中的文档：

| 代码变化 | 必查文档 |
| --- | --- |
| API、DTO、数据库结构、权限、状态机 | `backend-design.md` |
| 前台页面、按钮、表单和流程 | `operation-manual.md` |
| Seed 组件、Release、场景或环境 | `demo-catalog.md`、`组件分类规则.md` |
| 启动、测试、构建或部署脚本 | 根 `README.md`、`project-structure.md` |
| 版本或兼容策略 | `version-policy.md` 及所有文档顶部版本声明 |

合并前应执行 Markdown 相对链接检查、`git diff --check`，并把代码测试、构建、部署和真实环境验收分开报告。
