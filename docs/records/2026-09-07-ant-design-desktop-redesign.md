# Ant Design 桌面前端改造与本地验收

2026-09-07。Ant Design 固定为 6.6.3；1920×1080、浏览器 100% 缩放，Chromium。交付包含前端、隔离浏览器测试数据、测试与说明书；未部署、提交或推送。

## 实现

- 中文 ConfigProvider 与 Ant App 统一浅色主题、系统字体、按钮、表单、选择器、表格、标签、标签页、提示、通知与异步确认。移除在线字体和原生 confirm/prompt。
- 公共 Modal 使用 480 / 720 / 1040 / 1440px，正文独立滚动，标题与底部固定。表单防止重复提交，提交期间禁用关闭；确认默认聚焦取消，支持 Tab、Esc 与关闭后焦点恢复。更新阻断弹窗保持不可取消。
- 侧栏 224 / 64px，顶部 56px；组件目录 280px，运行列表 320px。组件、环境、运行与平台管理使用页内标签；场景画布与配置栏可折叠。详情标签保留编辑状态，资源定位链接不变。
- 保留 DAG、Playbook、日志、权限、业务校验与 API 请求构造。Ant Form 显式提取请求字段，参数仍区分 false、0、空值与数组；Upload 仅手动选择，不自动发请求。
- 说明书同步新的标签页及“更多操作”位置；路由继续懒加载。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| `pnpm --dir web test` | 27 个文件、278 项测试通过 |
| 最后焦点/版本阻断调整后的定向回归 | 3 个文件、21 项测试通过 |
| `./scripts/test-live-api-e2e.sh` | 全新临时 Go/SQLite API，9 项 E2E 通过（原有 4 项与新增 5 项） |
| `pnpm --dir web build` | TypeScript 与 Vite 生产构建通过 |
| `make check-docs` | 通过；API、路由与文档链接一致 |
| `git diff --check` | 通过 |

新增浏览器测试覆盖全部工作中心、各类表单与详情弹窗、长名称、长说明、多主机、历史版本、日志和多层弹窗；正文滚动时固定标题和底部坐标不变，页面无横向溢出，场景画布首屏可见高度至少 600px。Tab 包含正向循环与确认框循环，Esc、遮罩不可关闭、直接卸载后的焦点恢复、嵌套取消后的原按钮焦点和内容保留均有断言。

测试数据在临时数据库中隔离：组件、场景、环境、终态 Run、长日志和通知由 opt-in fixture 写入；Ansible 执行器为 false。灾备已配置状态由浏览器展示 fixture 提供，未创建仓库、备份或恢复数据库。生产 seeder 不注入这些数据。原生 beforeunload 继续承担浏览器离开提示。

业务回归包括角色可见性、同页对象切换、异步确认取消、重复删除防护、表单值类型、文件选择、合同审核、发布、环境版本、运行详情与等待状态。不同对象的表单隔离和标签切换保留内容有专门测试。

## 资源体积

单位为十进制 kB；每格为“原始 / gzip”。同一 Python gzip 方法比较构建文件，和 Vite 日志的压缩参数略有差异。

| 资源 | 改造前 | 改造后 |
| --- | ---: | ---: |
| 入口 JavaScript | 270.41 / 84.75 | 761.43 / 248.67 |
| 公共 CSS | 133.19 / 26.07 | 126.03 / 23.84 |
| 组件页 JavaScript | 134.07 / 38.29 | 135.04 / 38.91 |
| 场景页 JavaScript | 269.04 / 86.78 | 269.91 / 87.29 |
| 全部 JS 与 CSS | 1130.09 / 341.63 | 1963.29 / 617.15 |

全部资源 gzip 增加 275.52 kB。入口包含 Ant 的主题、表单和弹窗运行时；Vite 提示入口 chunk 超过 500kB。页面仍按原路由拆分，Table、Upload 等共享资源另有 chunk。此处记录构建体积，没有将本地测试等同于线上加载性能验收。

## 截图与原始证据

前后同为 1920×1080 的“新增环境维度类别”对比：

- [改造前](../../output/playwright/frontend-redesign-plan/create-category-before-1920.png)
- [改造后](../../output/playwright/frontend-redesign/after/create-category.png)

另存有改造前新建组件截图（1440px 宽，作为旧控件外观参考，不用于 1920 尺寸对比）。最终共有 39 张 1920×1080 截图，覆盖主页面、页内标签及代表性弹窗。

- [全部截图与本地验收记录](../../output/playwright/frontend-redesign/acceptance.md)
- [改造前资源清单](../../output/playwright/frontend-redesign/bundle-before.json)、[改造后资源清单](../../output/playwright/frontend-redesign/bundle-after.json)
- [前端测试日志](../../output/playwright/frontend-redesign/logs/frontend-tests.log)、[弹窗定向回归](../../output/playwright/frontend-redesign/logs/modal-regression.log)、[E2E 日志](../../output/playwright/frontend-redesign/logs/live-e2e.log)、[构建日志](../../output/playwright/frontend-redesign/logs/build.log)

本次未做部署、Git 提交或推送，也未进行真实主机执行、灾备恢复或生产性能验收。
