# Catalog 与数据库备份恢复

ClusterForge 使用两级恢复来源：SQLite 快照用于完整平台恢复，独立私有 Git Catalog 用于数据库快照也不可用时恢复已发布目录。发布事务不等待备份；最近成功清单代表可承诺的恢复点。

## 初始化

Environment Owner 可在“环境管理 → 发布目录灾备”输入允许根目录下的服务器路径。选择“创建私有仓库”时平台创建权限为 `0700` 的 bare Git 仓库及独立工作副本；选择“接入已有仓库”时路径必须已存在并包含 `catalog` 分支。允许根目录由 `CLUSTERFORGE_CATALOG_ALLOWED_ROOT` 配置，平台拒绝 `..` 和符号链接越界。

也可以由运维人员预先初始化仓库：

运维人员先创建只允许平台部署密钥写入的私有 Git 仓库，并初始化受保护的 `catalog` 分支。平台主机保存普通工作副本，不使用源码仓库：

```bash
install -d -m 0700 /var/lib/clusterforge/catalog-repo
git clone <private-catalog-url> /var/lib/clusterforge/catalog-repo
cd /var/lib/clusterforge/catalog-repo
if git show-ref --verify --quiet refs/remotes/origin/catalog; then
  git switch --track origin/catalog
else
  git switch --orphan catalog
  printf 'ClusterForge Catalog\n' > .gitkeep
  git add .gitkeep
  git -c user.name='ClusterForge Backup' -c user.email=backup@clusterforge.local \
    commit -m 'initialize catalog'
  git push -u origin catalog
fi
```

远端必须禁止 force-push 和删除 `backup/*` 标签。SSH 凭据由运行 `clusterforge-platform` 与 `clusterforge-backup` 的服务账号管理，不写入 `platform.env`。

配置 `/etc/clusterforge/platform.env` 后重启平台。六小时 Timer 不从
`CLUSTERFORGE_CATALOG_REPO` 猜测目标，而只读取 Environment Owner 通过前台保存的仓库选择；
前台尚未录入仓库时，平台会主动停止 Timer：

```bash
systemctl daemon-reload
systemctl restart clusterforge-platform
```

创建或接入仓库成功后，平台启用 `clusterforge-backup.timer`；服务重启时也会把 Timer
状态与前台选择重新对齐。若启停失败、最近一次异步备份失败或发布代次落后于最近恢复点，
Environment Owner 的“我的工作”和“发布目录灾备”面板都会显示告警与处置入口。

## 创建与检查恢复点

发布、联合发布和已发布对象废弃会在 30 秒窗口后异步触发快照。失败任务会保留错误并延迟重试，
不会回滚已经完成的发布。Timer 每六小时从前台选择文件重新解析仓库并兜底；没有有效选择时命令失败且
Timer 保持停止。部署或数据清理前应主动执行：

```bash
/opt/clusterforge/platform/clusterforge-backup snapshot --selected-repository --reason before-deploy
/opt/clusterforge/platform/clusterforge-backup list
/opt/clusterforge/platform/clusterforge-backup verify --backup-id <id>
```

`verify --verify-external` 还会完整下载 HTTP(S) 介质核对大小和 SHA-256，并用 `docker manifest inspect` 验证精确 OCI digest。该检查可能耗时且依赖 FSS、Registry 网络与认证，不属于本地备份成功门禁。

成功恢复点同时满足：SQLite `integrity_check`、`foreign_key_check`、数据库 SHA-256、Catalog/Playbook SHA-256、Git 分支 push 和 `backup/<backupId>` 标签 push。失败任务保持 `partial`，可执行：

```bash
/opt/clusterforge/platform/clusterforge-backup resume --backup-id <id>
```

## 恢复

Environment Owner 可在“发布目录灾备”选择 `backup/*` 恢复点并先执行预检。前台恢复采用严格空库模式：当前数据库只要存在任一组件或场景，就返回 `target_catalog_not_empty` 且不写入任何内容。确认执行时会重新核对 Git commit、Catalog SHA-256、Schema contract、数据库空库状态、发布代次和 `planDigest`。

空库恢复在单一 SQLite 事务中写入已发布/废弃的组件、Release、依赖、Action、场景 Revision、制品和镜像元数据。既有用户 ID 仅在名称与角色完全一致时复用；Playbook 仅在目标不存在或内容 SHA-256 完全一致时允许。ID/slug、用户属性或 Playbook 内容冲突都会整批失败，未提交的新文件会清理。成功后记录审计并异步触发新快照；Run、环境、审批、通知、Session 和既有审计历史不会从 Git 恢复。

CLI 恢复仍只写入不存在的新路径，适用于离线重建。不得把 `--target` 或 `--target-playbook-root` 指向在线数据库、当前作业根目录或其父目录。

先做只读计划：

```bash
clusterforge-backup restore-plan --backup-id <id>
```

优先恢复完整 SQLite 与对应 Playbook：

```bash
clusterforge-backup restore-db \
  --backup-id <id> \
  --target /var/lib/clusterforge/restore/platform.db \
  --target-playbook-root /var/lib/clusterforge/restore/jobs
```

仅当所有 SQLite 快照都不可用时，从 Git 恢复发布目录：

```bash
clusterforge-backup restore-catalog \
  --tag backup/<backupId> \
  --target /var/lib/clusterforge/restore/platform.db \
  --target-playbook-root /var/lib/clusterforge/restore/jobs
```

Catalog-only 恢复保留用户身份、组件/Release ID、依赖、动作、已发布/废弃状态、场景 Revision/DAG、Playbook 和内容身份；不会伪造 Draft、Run、审批、审计、环境 Revision 或安装回滚基线。完成校验后，仍须通过受保护部署流程停服、备份当前状态、切换数据库和 Playbook 根目录，再验证 HTTP、Schema contract、外键、资源数量和外部介质。

## 保留与失败边界

本地默认保留最近 24 小时内的全部成功快照、最近 14 个日恢复点、最近 8 个周恢复点，以及七天内的 partial 任务。Git commit 与 `backup/*` 标签不由平台自动删除。

Git 只保存介质和镜像的不可变身份与来源位置，不保存大型二进制内容。FSS、Registry、Git 远端和 SQLite 备份目录必须位于独立故障域，才能覆盖整机损坏。
