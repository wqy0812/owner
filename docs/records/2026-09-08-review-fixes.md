# 桌面改造与本地部署审核修复

本轮审核基线为 `d18870b`，范围为当前工作区的 Ant Design 桌面改造、前端测试拆分和本地 Docker 部署工具。修复以下四项问题，并按前端与本地部署两个范围提交。

## 修复与回归

- **下载被误当成页面跳转**：组件合同有未保存修改时，下载链接保留浏览器原生请求，避免 API 下载地址进入 SPA 路由。回归覆盖真实 Playbook helper 下载入口，以及下载后取消、确认离开页面时仍保留的合同保护。
- **子编辑器保存时仍能取消弹窗**：公共 Modal 的底部使用禁用的原生 fieldset 和 inert，覆盖显式 `disabled={false}` 的按钮与嵌入编辑器的 portal 操作。回归确认保存期间按钮、关闭入口和 Esc 被锁定，保存完成后操作恢复。
- **诊断定位到隐藏步骤**：诊断组件将步骤 ID 交给运行页；运行页先切回步骤标签，再滚动到目标。集成回归断言滚动发生时步骤可见，切换 Run 后恢复默认标签。
- **运行时定义修改后仍复用旧镜像**：每次部署均构建当前源码快照中的 `Dockerfile.runtime`，复用 Docker 层缓存。隔离测试覆盖已有旧镜像时连续修改定义，以及构建失败时禁止退回旧镜像继续部署。

## 提交前验证

| 检查 | 结果 |
| --- | --- |
| `pnpm --dir web test:coverage` | 44 个文件、299 项测试通过；无跳过或重试 |
| 覆盖率 | statements/lines 83.87%、branches 80.70%、functions 69.01%；原门槛不变 |
| `./scripts/test-live-api-e2e.sh` | 隔离 Go/SQLite API，1920×1080 Chromium，9/9 通过 |
| `pnpm --dir web build` | TypeScript 与 Vite 生产构建通过；保留已有的入口 chunk 大于 500kB 提示 |
| `go test ./...` | 全部包通过，使用 Go 测试缓存 |
| `make test-deploy-script test-fixture-boundary` | 部署门禁、10 项本地 Docker 脚本隔离测试与 fixture 边界检查通过 |
| `make check-docs` | API、页面路由和文档链接检查通过 |
| Git 差异检查 | 提交内容无空白错误 |

本地完整日志保存在 `output/review/2026-09-08/`。前端测试此前的稳定性复验是独立历史记录，见 [2026-09-07 验收](2026-09-07-frontend-test-stability.md)；本轮的 299 项结果对应加入修复后的代码。

Docker 脚本测试使用隔离替身，本轮未重建或切换实际容器，未推送、部署或进行真实主机验收。
