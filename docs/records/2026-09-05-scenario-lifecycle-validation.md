# 2026-09-05 场景生命周期本地验证

本记录对应当日工作区中的场景分支、版本演进、升级作业和业务验收实现。功能合同见[场景生命周期](../scenario-lifecycle.md)，操作流程见[平台说明书](../platform-manual.md)。原有未提交修改保持在工作区。

界面仅面向桌面浏览器，不要求移动端兼容。本轮没有部署应用、切换现有数据库或在真实集群上执行安装、升级、卸载及恢复，也没有提交或推送代码。

## 已验证的实现边界

| 范围 | 本地验证内容 |
| --- | --- |
| 创建与来源 | 空白首版；可见的跨 Owner 已发布来源分支；独立 Owner、身份及工作区；同场景正式成功且包含完整验收的来源；来源废弃和内容漂移拒绝；一个活动 Draft |
| 版本与工作区 | 同 Revision 继续编辑；运行中编辑阻塞；图及文件摘要前置条件；验收排序、类型化参数、节点及环境来源绑定；独立复制辅助文件；内容变化后证据失效 |
| 升级图 | 精确版本和发布线；显式幂等安装复用合同；有效参数变化；依赖迁移与增删顺序；旧参数卸载；无变化节点不安装；缺失能力、顺序歧义及冲突阻塞 |
| 执行 | 来源组件检查通过后才交付介质；动作及后置检查；全部目标检查；有序业务验收；任一失败立即停止；清理失败仍失败；凭据不进入公开日志和证据 |
| 基线与并发 | 实际环境基线与版本来源证明分别锁定；预览、提交、开始时复核；完整恢复链参与安装摘要；幂等并发只创建一次 Run；取消、重启、交付或动作失败保留部分变更 |
| 发布与恢复 | 首版安装证据、派生版双测试证据；完整步骤和验收覆盖；实际文件漂移阻塞发布；基线复核保留测试身份且不生成发布证据；历史 Run/回执不修改；新节点未清理不能仅凭旧节点复核恢复 |
| 离线转换与备份 | 只读源库并生成新库和工作区；完整性与外键检查；原有数据及历史摘要保留；目标冲突、活动作业、工作区漂移拒绝；Catalog 包含验收合同、辅助文件及来源关系 |

## 自动检查

以下检查通过。Go 缓存和 Ansible 依赖使用本轮临时目录；转换及 API 测试使用临时数据库，不连接现有测试环境。

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 全部包通过；未开启真实场景环境执行开关 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/service ./internal/store -run '^TestScenario' -count=1` | 通过 |
| 前端 `npm test -- --run` | 17 个测试文件、190 项测试通过；随后仅调整手册文字及对应断言，4 项手册定向测试与类型检查通过 |
| 前端 `npm run build` | 类型检查及构建通过；已包含更新后的功能说明 |
| `make check-docs` | API 路由、页面、相对链接和数据库合同检查通过 |
| `make test-deploy-script` | 部署脚本策略测试通过，没有执行部署 |
| `make test-fixture-boundary` | 测试夹具边界检查通过 |
| `git diff --check` | 通过 |

另使用 Ansible Core 2.18.6、Python 3.11.15 在本机临时目录运行真实 Ansible 作业：

```bash
CLUSTERFORGE_JOB_TEST_ANSIBLE=/private/tmp/clusterforge-scenario-ansible/bin/ansible-playbook \
GOCACHE=/private/tmp/clusterforge-scenario-go-cache \
go test ./internal/ansible ./internal/service \
  -run 'TestRoleJobRealAnsible|TestRoleJobAcceptance|TestScenarioBusinessAcceptanceRealAnsible|TestScenarioAcceptanceCredentialIsolationRealAnsible' \
  -count=1
```

两个包均通过。覆盖组件 Role 与场景验收串行执行、后续作业停止、成功清理、清理失败保留现场，以及主动失败消息中的凭据脱敏。所有主机连接均为本机测试连接，涉及文件均在临时目录。

## 桌面浏览器

使用隔离的本地 API、临时数据库与测试作业协议准备证据。浏览器验证了分支创建、新建版本、组件版本更换、验收录入与排序、辅助文件、参数绑定、升级预览、两类测试证据、发布、正式升级入口和 Run 步骤展示。浏览器实际发布了临时数据中的目标版本；正式升级只到预览，没有提交执行。

验证包含 1366×900、1440×1050，以及开发指南要求的 1280×800、1440×900、1920×1080 桌面窗口。检查导航、表格滚动、DAG、检查器和弹窗操作可达性，不以移动端布局作为验收要求。1280×800 下节点表通过容器横向滚动访问最后一列，检查器底部按钮和分支弹窗操作均可达；三个规定尺寸均无整页横向溢出。

浏览器检查发现并修复了发布后仍可保留旧测试预览、聚合场景 Run 的空动作值导致运行中心读取失败，以及组件版本替换时依赖选择需要明确确认等问题。最终页面无控制台错误。临时 API 服务和本轮浏览器会话验证后关闭。

本机截图位于 `output/playwright/`，包括 `scenario-new-version-desktop.png`、`scenario-release-replacement-desktop.png`、`scenario-formal-upgrade-desktop.png`、`scenario-parameter-binding-desktop.png`、`scenario-acceptance-1366.png` 和 `scenario-upgrade-run-desktop.png`。临时自动测试日志保存在 `/private/tmp/clusterforge-scenario-*.log`，前端明细为 `/private/tmp/clusterforge-ui-all-final.json`；这些是本轮本机证据，不作为仓库中的永久运行证据。

三个规定桌面尺寸另有九张布局截图，命名为 `scenario-desktop-1280x800-dag.png` 等，尺寸分别为 `1280x800`、`1440x900`、`1920x1080`，视图分别为 `dag`、`table`、`modal`。

## 后续环境验证

真实环境的数据转换、部署与升级另行安排。届时应重新核对实际环境基线、来源正式 Run、精确组件合同、审批和恢复备份，使用当前预览创建执行。此记录不能代替真实环境中的安装测试、升级测试或业务验收证据。
