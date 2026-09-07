# 组件前端与构建交付模块化验收

> 日期：2026-09-07；范围：[实施方案](component-ui-and-delivery-modularization-plan-2026-09-06.md)的 F1–F3、B1–B3。
> 状态：本地实施与验收完成；未提交、未推送、未部署，未访问真实集群。

## 实施结果

- `ComponentsPage.tsx` 从 1824 行收敛到约 260 行，负责公共查询、选中身份、离开保护和页面组合。目录、只读详情、合同、动作、工作区、介质、验证、导入和版本操作各自拥有职责与本地状态。
- `useComponentSelection` 管理深链消费和定位。版本操作按用户、组件、Release 隔离；关闭预览后忽略旧响应，切换目标后旧恢复操作不会改变新的选择。
- 公共类型移至 `types/runRetention.ts`、`types/componentUsage.ts`；最终导入审计还发现 `PreparationRequest`、`PreparationSession` 被 API 从 UI 引用，已一并移至 `types/executionPreparation.ts`。场景纯函数与组件导入解析分别归入对应 feature。
- `internal/imagebuild.Docker` 负责临时目录、Dockerfile、build → push → inspect、stdout/stderr、取消与清理。输出按行保留有界前缀，同时持续排空长输出；Catalog 继续负责权限、状态、事务、审计及事件。
- `internal/delivery` 统一介质类型、镜像身份、HTTP/FSS 和 Docker 交付实现。平台保留探测观测包装器，Catalog 与 Delivery 共用包装实例；缓存按来源、身份及适配器实例隔离，不跨不同访问配置复用。默认与显式注入均经过包装，同一包装不会再次包装。
- CLI 只读取 Metadata 的介质投影，完整 JobPlan 摘要由 `ansible.PlanDigest` 负责。移除了 `service.DigestStandalonePlan`、`service.PrepareStandaloneJobMedia` 及旧适配器入口，没有保留转发导出。

## 合同与验证

| 检查 | 结果 |
| --- | --- |
| Go 全量 | `go test ./... -count=1` 通过 |
| 静态检查 | `go vet ./...` 通过 |
| 并发回归 | service、api、delivery、imagebuild、jobcli 全包 `-race` 通过 |
| 前端全量 | 24 个测试文件、240 项测试通过；最终类型归位和工作区追加修改后，受影响的 4 文件、139 项测试再次通过 |
| 前端构建 | TypeScript 检查与 Vite production build 通过 |
| 二进制 | server、backup、clusterforge-job 构建通过 |
| 真实本地 Ansible | `make test-role-job` 通过，使用 ansible-core 2.18.6、Python 3.12.14；覆盖原生 Role、阶段、场景验收、独立 run/resume/rollback、控制器中断 |
| 来源验证与介质顺序 | 新增 `TestStandaloneRealMediaWaitsForSourceVerification` 真实执行通过：来源失败时 0 个介质请求；来源成功后目标 Probe → Transfer → Probe，共 3 个请求 |
| 构建与平台记录 | 假 Docker 验证顺序、失败中止、完整输出、取消、目录清理；平台测试证明运行记录失败不启动构建、失败不登记镜像、取消记录 interrupted；已有构建 API 测试通过 |
| JSON 与摘要 | 使用提取前类型生成的 nil、空切片、混合介质 JSON 基线及完整 JobPlan 摘要，验证字段顺序、tag、省略规则和摘要不变 |
| 导入方向 | 组件 feature 导入图无环；feature 不导入 pages；API/types 不导入 UI；CLI 依赖图不含 service/store/api 或 SQLite；新 Go 包不反向依赖这些业务包 |
| 文档与空白 | `make check-docs`、`git diff --check` 通过 |

Go 全量和真实 Ansible 的首次沙箱运行被本地监听端口/控制套接字限制阻断，随后获得本机测试通信权限重跑通过。测试使用临时数据库、临时文件站和 localhost 目标；镜像构建使用假 Docker，不代表真实 Registry 推送验收。

## 浏览器与请求证据

使用隔离 `browser-consumer` / `browser-upstream` 夹具。现有 `parameter-contract.live.spec.ts` 对真实本地 API 执行通过，确认参数来源、公开/内部切换、值负责人和保存入口。额外打开镜像构建弹窗并检查桌面布局。合同保存、深链、身份、Playbook、已完成构建日志、环境锁定及发布门禁由 App 等行为测试覆盖。

改造前源码通过临时 Vite 实例读取同一夹具，与改造后对照：

| 首次目录读取 | 改造前 | 改造后 |
| --- | --- | --- |
| 组件摘要成功请求 | 1 | 1 |
| 当前组件详情成功请求 | 1 | 1 |
| 当前组件直接引用成功请求 | 1 | 1 |
| 上述开发模式取消请求 | 各 1 | 各 1 |

身份初始化、平台目录读取次数也一致；没有初始全量 runs、workbench 或组件合同读取。开发模式的取消请求来自 React StrictMode。静态模块请求由 54 增至 68，这是 Vite 按源码模块加载的变化，生产版仍经打包。两侧控制台均只有新会话预期的 session/me 401 与已有 favicon 404，无新应用异常。

本机截图与请求原始记录：

- `output/playwright/modular-before-1440.png`
- `output/playwright/modular-after-1440.png`
- `output/playwright/modular-after-1280.png`
- `output/playwright/modular-image-1280.png`
- `/private/tmp/clusterforge-modular-20260907/browser-requests-before.txt`
- `/private/tmp/clusterforge-modular-20260907/browser-requests-after.txt`

当前工作区原有管理页、样式、测试及实施期间出现的环境回滚改动均保留；本记录只描述本次模块化交付。数据库结构、HTTP 路由及历史 Release/Run、作业包、回执没有改写。
