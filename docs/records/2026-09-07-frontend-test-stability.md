# 前端测试稳定性与结构优化验收

## 范围与归属

本次在当前工作区的 Ant Design 桌面界面上优化测试组织和测试设施，不改变公共 API、领域类型、数据库契约。没有把原始 37 项失败认定为 37 个业务缺陷。

原 App 测试的 123 个展开用例映射为 131 个用例：平台目录长流程拆出 3 个用例，Playbook 长流程拆出 5 个用例。其余 155 个现有用例保留，新增 6 个异步设施与 fixture 契约自检、4 个组件介质回归，全套共 296 项。

逐例映射见 [case-mapping.json](2026-09-07-frontend-test-case-mapping.json)，包括原始行号、19 个超时和 18 个查询失败的归属。原始文件快照保存在 `output/playwright/frontend-test-stability/App.before.txt`。

| 测试边界 | 文件（均位于 `web/src/test/`） |
| --- | --- |
| 身份初始化、路由、跨页面操作 | `App.test.tsx` |
| 目录读取与组件合同 | `ComponentCatalog.test.tsx`、`ComponentContract.test.tsx` |
| 组件验证与 Playbook、媒体 | `ComponentVerification.test.tsx`、`PlaybookIntegration.test.tsx`、`ArtifactModal.test.tsx` |
| 平台目录与工作台审核 | `PlatformManagement.test.tsx`、`DashboardPage.test.tsx` |
| 环境配置、历史删除与灾备 | `EnvironmentsPage.test.tsx`、`EnvironmentHistory.test.tsx`、`DisasterRecoveryPage.test.tsx` |
| 场景、运行与通知 | `ScenariosPage.test.tsx`、`RunsPageIntegration.test.tsx`、`NotificationsPage.test.tsx` |
| 刷新合并、SSE 重连与计时器清理 | `AppRefresh.test.tsx`，以及原有 `useApiData`、`useRunActivity` hook 测试 |

页面测试只挂载目标页面、路由与必要 Provider。Playbook 长流程直接挂载真实 `EditReleaseModal`，保留 API 序列化和重新读取持久化值。CredentialRef 验证同时检查 Action 原子保存和 Draft 保存的显式空数组，并重新打开编辑器确认已清空。

## 测试设施

- `fixtures/appFixtures.ts` 为每例重建可变数据；目录 summary、detail、contracts 分别返回当前读模型，未匹配请求直接报错。未知写操作不能落入默认读取响应。
- `interactions.ts` 每例新建 `userEvent.setup()`。大表单的整字段输入使用粘贴，逐键交互由控件测试与真实浏览器测试覆盖。
- `antdInteractions.ts` 从当前 Select 的 `aria-controls` 找到对应 listbox。确认操作等待捕获的弹窗卸载；菜单限制到可见触发器与当前菜单。
- `testLifecycle.ts` 管理受控异步任务，默认支持 AbortSignal。迟到响应测试显式保留并最终结算；卸载后仍未完成的 fixture 任务统一中止。
- `setup.ts` 在 `act` 内卸载并结算任务，然后检查 SSE 已关闭、没有未匹配请求及非预期 React 异步更新警告，最后恢复 mock、全局对象、计时器和存储。Vitest 自身的未处理异常检查保持启用。
- Ant 的 CJS `useId` 在 test 环境返回相同 `test-id`。测试仅将该 hook 替换为 React 的真实唯一 ID，并在文件结束恢复，避免全局切换 `NODE_ENV=development`。这是针对当前锁文件版本的测试适配；依赖升级时应复核。

性能采样中，组件依赖流程的 CPU 开销主要来自 jsdom 下的可访问性查询：`queryAllByRole` 累计约 2.57 秒，`computeAccessibleName` 约 2.52 秒。将查询限制到当前编辑区域、使用已有 label，并减少重复全页面扫描；保留可访问性和业务断言。

组件介质补充真实 API 序列化测试：选定 FILE_STATION 环境上传并提交 SHA-256、登记失败保留输入且重试成功只替换同名别名、已发布版本修复来源 URL 的取消与保存、解除引用失败与重试。第一次完整覆盖率虽 292 项用例全过，但 statements/lines 82.68% 未达 83%，因此补充此缺口后重新验收；未降低门禁。

## 重复运行方式

```sh
node web/scripts/test-stability.mjs output/playwright/frontend-test-stability/acceptance
./scripts/test-live-api-e2e.sh
pnpm --dir web build
make check-docs
git diff --check
```

验收脚本按顺序执行普通全套三次、完整覆盖率两次，第二次覆盖率固定随机种子 `20260907`，文件和用例均洗牌。使用同一 `vitest.config.ts`：文件隔离、1–2 worker、默认 5 秒。没有跳过、自动重试、单例额外加时或降低覆盖率阈值。阈值保持 statements/lines 83%、branches 77%、functions 62%。

每轮保存完整日志、JSON 逐例状态与耗时。`manifest.json` 保存精确命令、配置、Node/pnpm 版本、Git HEAD、源码 SHA256，并在每轮结束检查源码没有改变；执行失败或源码改变立即停止验收。`preflight/` 是源码发生过修改的诊断结果，不计入连续通过次数。

隔离 API E2E 使用项目现有脚本生成临时数据库并启动独立 Go 服务，浏览器视口为 1920×1080。测试结束清理临时服务和数据；此结果不代表部署或真实 Ansible 主机验收。

## 验收结果

全部 5 轮通过，43 个测试文件、296 项用例，0 跳过、0 重试。5 轮源码 SHA256 均为 `51aa791f1240575ca763a6ea78a97f11e3115d9ac764bcb03583caab91c962b7`。Node v24.19.0、pnpm 11.19.0、Vitest 2.1.9。

| 运行 | 结果 | Vitest 总耗时 | 最慢单例 |
| --- | --- | --- | --- |
| 普通 1 | 296/296 | 73.13s | 2.07s |
| 普通 2 | 296/296 | 72.24s | 2.00s |
| 普通 3 | 296/296 | 72.95s | 2.05s |
| 覆盖率 1 | 296/296 | 132.38s | 4.45s |
| 覆盖率 2，seed=20260907 | 296/296 | 147.73s | 4.06s |

两轮完整覆盖率均为：statements/lines **83.74%**、branches **80.31%**、functions **68.71%**。原始 37 项失败已在映射文件中全部获得五轮通过记录。

重点复验包括：120 候选中最多提交 100 个审批 ID，CredentialRef 显式空数组保存并重新打开，多个 Playbook Action 的 kind/ID/内容及回滚来源和目标绑定，场景保存成功/失败/冲突期间的编辑保留，环境历史删除的引用限制、冲突重预览和取消零写入。

覆盖率下 CredentialRef 完整持久化流程最长 **1.99s**；批量审批边界最长 **1.94s**。Playbook 长流程拆成独立验证，新增 rollback Action 用例最长 **3.34s**；不能把拆分后的单例耗时当作原完整流程耗时。原普通诊断 159.00s、原覆盖率失败运行 327.42s 含超时等待，和本次通过运行属于不同工作量，只作为诊断对照。

现有隔离 API E2E **9/9 通过，28.4s**，视口 1920×1080。该套件包含 3 项网络 mock 交互测试及真实 Go fixture 页面测试；灾备显示和版本更新提示使用明确的展示 fixture。通过了弹窗尺寸与固定操作区、Tab 焦点限制及恢复、Esc/嵌套确认取消、下拉关闭、页签和场景画布检查，并人工查看嵌套确认和 Playbook 编辑器截图。

验收数据：

- [逐轮汇总](2026-09-07-frontend-test-results.json)
- [原始用例及逐轮复验映射](2026-09-07-frontend-test-case-mapping.json)
- 原始失败日志：`output/deploy/20260907-214436/local-gates.log`
- 正式配置、源码哈希、命令、完整日志、逐例 JSON 与覆盖率 HTML：`output/playwright/frontend-test-stability/acceptance/`
- 浏览器日志与截图：`output/playwright/frontend-test-stability/live-api-e2e.log`、`output/playwright/frontend-test-stability/screenshots/`
- 补覆盖前的 292 项通过但覆盖率不足记录：`output/playwright/frontend-test-stability/coverage-gap/`

`pnpm --dir web build` 通过（含 TypeScript 构建），Vite 用时 2.75s；保留现有大于 500kB 的 chunk 提示，主入口 761.43kB。`make check-docs` 与 `git diff --check` 通过。日志分别保存在同一证据目录的 `build.log`、`check-docs.log`。

本次没有修改产品代码、公共 API、领域类型或数据库契约；没有提交、推送或部署，原工作区已有改动保留。
