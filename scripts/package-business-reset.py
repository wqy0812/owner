#!/usr/bin/env python3
"""Package an already sanitized business reset bundle; never opens the live DB."""
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import tarfile

bundle = Path(sys.argv[1]).resolve()
os.umask(0o077)
data = json.loads((bundle / 'business.json').read_text())['tables']
root = Path('/opt/clusterforge/jobs')
if root.is_symlink() or not root.is_dir():
    raise SystemExit('Unexpected Playbook root')
files = []
for path in sorted(root.rglob('*')):
    if '.component-import-staging' in path.parts:
        continue
    if path.is_symlink():
        raise SystemExit('Playbook symlink requires explicit review: ' + str(path))
    if path.is_file():
        files.append(path)
with tarfile.open(str(bundle / 'playbooks.tar.gz'), 'w:gz') as archive:
    for path in files:
        archive.add(str(path), arcname=str(path.relative_to(root)), recursive=False)
playbook_hashes = {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
(bundle / 'playbook-sha256.json').write_text(json.dumps(playbook_hashes, indent=2, ensure_ascii=False))

# Only explicit credential files and the service account's known_hosts; no
# whole-home-directory or run-workspace archives.
private = {Path('/etc/clusterforge/platform.env'), Path('/root/.ssh/known_hosts'), Path('/opt/clusterforge/platform/.env'), Path('/root/.docker/config.json')}
for line in Path('/etc/clusterforge/platform.env').read_text().splitlines():
    if line.startswith('NEWPLATFORM_SSH_KNOWN_HOSTS='):
        private.add(Path(line.split('=', 1)[1].strip()))
missing = []
for revision in data['environment_revisions']:
    for ref in revision.get('credential_refs_json', []):
        if ref.get('kind') == 'sshKeyPath':
            p = Path(ref.get('reference', ''))
            if p.is_absolute() and p.is_file():
                private.add(p)
            else:
                missing.append(ref.get('name', 'unnamed'))
with tarfile.open(str(bundle / 'sensitive-files.tar.gz'), 'w:gz') as archive:
    for path in sorted(private):
        if path.is_file():
            # Dereference only this exact, explicitly named credential file.
            content = path.read_bytes()
            import io
            info = tarfile.TarInfo(str(path).lstrip('/'))
            info.size = len(content)
            info.mode = 0o600
            archive.addfile(info, io.BytesIO(content))

counts = {key: len(value) for key, value in data.items()}
(bundle / 'counts.json').write_text(json.dumps(counts, indent=2, ensure_ascii=False))
components = {r['id']: r for r in data['components']}
releases = {r['id']: r for r in data['component_releases']}
users = {r['id']: r for r in data['users']}
lines = ['# 手工重新录入资料', '', '本资料不包含历史 Run、日志、审批、安装记录或测试证据。旧版本和发布状态仅供参考，重新录入后必须重新测试。', '',
'## 文件说明', '', '- `business.json`：全部业务定义与历史版本，JSON 字段已展开。', '- `platform.db`：不含 Run 历史的业务恢复库，仅用于故障恢复，不自动导入新平台。', '- `foundation.db`：按当前 schema 新建，仅转换账号、分类/选项和环境变量字段；不含旧业务、Run 或全局默认参数。', '- `playbooks.tar.gz`：完整 Playbook 文件，路径相对于原 `/opt/clusterforge/jobs`。', '- `playbook-sha256.json`：每个 Playbook 文件的摘要。', '- `sensitive-files.tar.gz`、`platform.env`：受限运维配置和凭据文件，不要公开分享。', '- `SHA256SUMS`：全部交付文件的校验清单。', '',
'## 录入顺序', '', '1. 核对保留的账号、平台选项和参数目录。', '2. 按依赖顺序建立组件，填写参数、Action、环境约束，上传 Playbook 并登记介质和镜像。', '3. 按环境资料录入主机、主机组、Facts、参数、变量与凭据引用。', '4. 建立场景，重新选择新录入的组件版本，连接依赖和参数映射。', '5. 重新检查和测试；真实主机原有软件未被卸载，不要将平台空库当作主机空环境。', '',
'## 数据数量', '', '| 类别 | 数量 |', '|---|---:|']
for key, count in counts.items(): lines.append('| {} | {} |'.format(key, count))
lines += ['', '## 组件与版本', '']
for cid, component in components.items():
    owner = users.get(component['owner_id'], {}).get('name', component['owner_id'])
    lines += ['### ' + component['name'], '', '- Owner：' + owner, '- 原组件 ID：`' + cid + '`']
    for rid, release in releases.items():
        if release['component_id'] != cid: continue
        lines += ['- 版本：{}；原状态：{}；资料索引：`{}`'.format(release['version'], release['status'], rid)]
        for dep in data['component_dependencies']:
            if dep['release_id'] == rid:
                upstream = releases.get(dep['upstream_release_id'], {})
                name = components.get(dep['upstream_component_id'], {}).get('name', dep['upstream_component_id'])
                lines += ['  - 依赖：{} / {}；原版本 ID：`{}`'.format(name, upstream.get('version', ''), dep['upstream_release_id'])]
    lines += ['']
lines += ['## 场景与环境', '']
for kind, table in [('场景','scenarios'),('环境','environments')]:
    for item in data[table]: lines.append('- {}：{}；原 ID：`{}`'.format(kind,item['name'],item['id']))
if missing: lines += ['', '原环境中以下凭据引用对应文件缺失，需要重新配置：' + ', '.join(missing)]
lines += ['', '旧 ID 仅用于在资料中定位关系。前台录入生成新 ID 后，依赖和场景必须选择新对象。', '', '既有非 Git 历史备份未复制到本目录；Git 备份仓库不在本资料包中。']
(bundle / '重新录入清单.md').write_text('\n'.join(lines) + '\n')
# File package hashes are checked on the server and again after the local copy.
with (bundle / 'SHA256SUMS').open('x') as manifest:
    for path in sorted(bundle.iterdir()):
        if path.is_file() and path.name != 'SHA256SUMS':
            manifest.write(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + path.name + '\n')
print(json.dumps({'counts': counts, 'playbook_files': len(files), 'credential_files': len(private), 'missing_credential_files': len(missing)}, ensure_ascii=False))
