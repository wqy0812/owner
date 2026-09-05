# Catalog 与数据库备份恢复

ClusterForge 使用两级恢复来源：SQLite 快照、对应 Playbook 与 Run 归档文件共同用于完整平台恢复，独立私有 Git Catalog 用于数据库快照也不可用时恢复已发布目录。发布事务不等待备份；最近成功清单代表可承诺的恢复点。

## 初始化

平台服务必须先设置 `CLUSTERFORGE_BACKUP_ENABLED=true`；否则状态页会明确显示服务端能力未启用，创建、接入和备份入口均保持关闭。启用能力不等于已经选择仓库。

环境 Owner 可在“灾备目录 → 发布目录灾备”输入允许根目录下的服务器路径。选择“创建私有仓库”时平台创建权限为 `0700` 的 bare Git 仓库及独立工作副本；当前发布目录非空且尚未选择仓库时可“接入已有备份仓库”，已配置后入口改为“更换备份仓库”。已有仓库必须包含 `catalog` 分支。允许根目录由 `CLUSTERFORGE_CATALOG_ALLOWED_ROOT` 配置，平台拒绝 `..` 和符号链接越界。

当组件和场景均为空且尚未选择仓库时，页面把接入与灾难恢复合并为“从已有 Git 仓库恢复”向导：先验证并保存仓库，再选择远端恢复点、预检并确认。向导仍允许“仅接入，暂不恢复”；仓库接入与数据恢复在服务端保持为两个独立操作和审计事件。

也可以由运维人员预先初始化仓库：

运维人员先创建只允许平台部署密钥写入的私有 Git 仓库，并初始化受保护的 `catalog` 分支。平台主机保存普通工作副本，不使用源码仓库：

```bash
install -d -m 0700 /var/lib/clusterforge/catalog-repo
git clone <private-catalog-url> /var/lib/clusterforge/catalog-repo
cd /var/lib/clusterforge/catalog-repo
if git show-ref --verify --quiet refs/remotes/origin/catalog; then
  git checkout --track origin/catalog
else
  git checkout --orphan catalog
  printf 'ClusterForge Catalog\n' > .gitkeep
  git add .gitkeep
  git -c user.name='ClusterForge Backup' -c user.email=backup@clusterforge.local \
    commit -m 'initialize catalog'
  git push -u origin catalog
fi
```

远端必须禁止 force-push 和删除 `backup/*` 标签。SSH 凭据由运行 `clusterforge-platform` 与 `clusterforge-backup` 的服务账号管理，不写入 `platform.env`。

配置 `/etc/clusterforge/platform.env` 后重启平台。在线备份不从
`CLUSTERFORGE_CATALOG_REPO` 猜测目标，而只读取环境 Owner 通过前台保存的仓库选择：

```bash
systemctl daemon-reload
systemctl restart clusterforge-platform
```

创建或接入仓库成功后，平台启用发布后异步备份和前台立即备份。若最近一次异步备份失败或发布代次与最近恢复点不一致，
环境 Owner 的“我的工作”和“发布目录灾备”面板都会显示告警与处置入口。

## 创建与检查恢复点

平台采用两种备份入口：发布、联合发布和已发布对象废弃会在 30 秒窗口后异步触发快照；
环境 Owner 也可在“灾备目录 → 发布目录灾备”单击“立即备份”。
失败任务会保留错误并延迟重试，不会回滚已经完成的发布。没有有效仓库选择时，立即备份
按钮也不可用。部署或数据清理前，应由环境 Owner 在前台创建恢复点，并等待页面显示新的
`backup/*` 标签与 Git commit 后再继续。

人工创建恢复点不再提供 CLI `snapshot` 命令。CLI 仅保留恢复点检查、失败 push 续传和离线恢复：

```bash
/opt/clusterforge/platform/clusterforge-backup list
/opt/clusterforge/platform/clusterforge-backup verify --backup-id <id>
```

受保护部署仍使用受限的 `automation-snapshot --source <固定来源>` 入口；它不接受人工 reason，
不作为日常手动备份界面。

`verify --verify-external` 还会完整下载 HTTP(S) 介质核对大小和 SHA-256，并用 `docker manifest inspect` 验证精确 OCI digest。该检查可能耗时且依赖 FSS、Registry 网络与认证，不属于本地备份成功门禁。

已配置 `CLUSTERFORGE_RUN_ARCHIVE_DIR` 时，完整快照同时复制该数据库引用的全部归档文件，在清单中保存 Run ID、相对文件名、SHA-256 和大小，并验证归档内容；文件缺失或损坏会使快照/校验失败。Git Catalog 本身不承载 Run 归档。

成功恢复点同时满足：SQLite `integrity_check`、`foreign_key_check`、数据库 SHA-256、Catalog/Playbook SHA-256、Git 分支 push 和 `backup/<backupId>` 标签 push。失败任务保持 `partial`，可执行：

```bash
/opt/clusterforge/platform/clusterforge-backup resume --backup-id <id>
```

受保护部署使用 `--rebuild-v1-db` 重建数据库时，会在新数据库通过 Schema contract 与外键检查后通过自动化入口强制创建并确认新的恢复点。常规部署只接受当前精确合同，并在停止服务前创建恢复点；任一恢复点失败都会触发部署回滚。应用内 Reset 保持发布代次单调递增；启动补偿还会同时比较发布代次与 Schema contract，避免旧合同仓库清单掩盖待备份状态。

## 恢复

环境 Owner 可在“发布目录灾备”选择远端仍存在的 `backup/*` 恢复点并先执行预检；本地残留但远端已删除的标签不会展示，也不能绕过恢复门禁。页面会先显示组件和场景数量，目录非空时直接关闭恢复按钮。前台恢复仍采用严格空库模式：当前数据库只要存在任一组件或场景，就返回 `target_catalog_not_empty` 且不写入任何内容。确认执行时会重新核对 Git commit、Catalog SHA-256、Schema contract、数据库空库状态、发布代次和 `planDigest`。

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

`restore-db` 当前恢复数据库和 Playbook，不会自动复制或切换 Run 归档目录。有已归档 Run 时，还必须从同一已校验恢复点复制 `archives/` 到新的持久化目录，并将 `CLUSTERFORGE_RUN_ARCHIVE_DIR` 指向该目录；保持归档文件名不变。恢复后抽查受权限保护的下载，不能仅以数据库完整性通过判定历史恢复完整。

仅当所有 SQLite 快照都不可用时，从 Git 恢复发布目录：

```bash
clusterforge-backup restore-catalog \
  --tag backup/<backupId> \
  --target /var/lib/clusterforge/restore/platform.db \
  --target-playbook-root /var/lib/clusterforge/restore/jobs
```

Catalog-only 恢复保留用户身份、组件/Release ID、依赖、动作、已发布/废弃状态、场景 Revision/DAG、Playbook 和内容身份；不会伪造 Draft、Run、审批、审计、环境 Revision 或安装回滚基线。完成校验后，仍须通过受保护部署流程停服、备份当前状态、切换数据库和 Playbook 根目录，再验证 HTTP、Schema contract、外键、资源数量和外部介质。

Catalog 导入只接受当前完整表结构，包括环境参数默认值表；不补齐旧格式缺失的表，也不转换历史数据库或旧参数结构。

Catalog 恢复后首次启动会保留恢复的选项目录，并单独初始化空的环境变量字段目录，提供 `IMAGE_REGISTRY` 和 `FILE_STATION`。已有变量字段不会被覆盖；完成初始化后，管理员删除的字段也不会在后续重启时重新出现。

## 独立运行历史备份

Git 备份未启用时，运行历史仍可通过独立命令导出到新目录：

```bash
clusterforge-backup database history-snapshot --db /path/platform.db --archive-dir /persistent/run-archives --target /backup/new-history
clusterforge-backup database history-verify --db /backup/new-history
clusterforge-backup database history-restore --db /backup/new-history --target /restore/new-history
```

该入口保存数据库一致快照、数据库引用的归档包及校验清单，不依赖 Git。恢复后应在维护窗口配置新的数据库路径和 `CLUSTERFORGE_RUN_ARCHIVE_DIR`；Playbook、FSS、Registry 和凭据引用所指资源仍需独立保留。数据库单文件备份不能恢复已经移出数据库的历史日志。完整策略和下载/清理规则见 [运行历史管理](run-history-and-adaptation.md)。

## 部署数据库检查与文件备份

受保护部署使用 `clusterforge-backup database`，复用项目内置的 Go SQLite 引擎，支持当前结构中的生成列，不依赖主机 Python SQLite。该入口与 Catalog 恢复点分开：只读取指定数据库，或将一致性快照写入全新路径；不初始化源库、不迁移结构、不覆盖已有文件。

```bash
clusterforge-backup database contract --db /var/lib/clusterforge/platform.db
clusterforge-backup database active-runs --db /var/lib/clusterforge/platform.db
clusterforge-backup database verify --db /var/lib/clusterforge/platform.db \
  --expected-contract clusterforge-v1-20260905-adaptation-run-archive
clusterforge-backup database snapshot --db /var/lib/clusterforge/platform.db \
  --target /path/to/new-backup.db
```

文件备份包含已提交 WAL，完成后检查完整性和外键，保留源文件权限与归属。部署在停服后再次检查活动 Run，再创建备份；任何检查或激活失败都应恢复备份中的原数据库和原二进制。

## 保留与失败边界

本地默认保留最近 24 小时内的全部成功快照、最近 14 个日恢复点、最近 8 个周恢复点，以及七天内的 partial 任务。Git commit 与 `backup/*` 标签不由平台自动删除。

Git 只保存介质和镜像的不可变身份与来源位置，不保存大型二进制内容。FSS、Registry、Git 远端和 SQLite 备份目录必须位于独立故障域，才能覆盖整机损坏。

## 业务清空专用备份（不含 Run 历史）

仅在明确授权清空业务数据时使用。`database business-snapshot` 从只读源库把白名单业务表复制到全新数据库，不会先复制完整库再删除 Run；未知表、活动 Run、活动镜像构建、完整性或外键错误均阻止导出。历史会话、运行记录、审批、通知、审计、构建及检查记录、安装回滚基线不进入新文件，仅重建用于避免基础目录重复初始化的固定标记。测试中或测试通过的场景转为 Draft，测试时间清空。`foundation-snapshot` 用当前 schema 创建全新基础库，仅复制旧 V1 账号、平台分类/选项（含父子关系及退役标记）与环境变量字段定义。它不复制旧 schema、组件、场景、环境、Run、会话、全局参数定义/默认值或旧自动保留策略；新库自动归档和清理保持关闭。源库目录字段不满足当前约束时拒绝转换，不猜测映射。业务恢复库继续保留源合同，仅用于部署失败回退。

停服准备使用 `scripts/prepare-business-reset.sh <远端全新目录> <新备份工具路径> <package-business-reset.py路径>`。它持有部署和备份锁，禁用 Git 自动备份，生成业务恢复库、基础库、JSON、完整 Playbook 包、受限凭据包及录入清单。成功后保持停服；准备失败会尝试启动原服务，但不会重新开启 Git 备份。

将整个目录复制到本机，分别核对 `SHA256SUMS`，验证两个数据库，然后在远端目录创建 `local-copy.verified` 标记（内容为已核对的 `SHA256SUMS` 文件 SHA-256）。通过 `scripts/deploy-test-88-55.sh --business-reset-dir /var/lib/clusterforge/business-reset-backups/<编号>` 部署当前工作区。该模式要求服务已停止、源数据库摘要未变化、备份通过业务/基础库验证；使用当前合同基础库初始化，固定 `NEWPLATFORM_SEED_PROFILE=identities`，防止启动时自动录入示例业务；失败回退原程序和无 Run 的业务库。它不调用完整数据库快照或 Git Catalog 快照。

普通部署如明确停用 Git 备份可使用 `--disable-catalog-backup`；未指定时保留原有 Git 备份门禁。专用 Git 仓库删除必须在部署验收后按已确认的仓库路径及对应工作副本精确执行；该部署选项本身不删除仓库，也不清理旧备份、真实主机、文件站或镜像仓库。
