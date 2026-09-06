#!/usr/bin/env python3
"""Validate the directory sample on localhost only; requires Ansible and PyYAML."""
import json, os, pathlib, shutil, subprocess, tempfile, uuid, yaml
sample=json.loads((pathlib.Path(__file__).resolve().parents[1] / 'examples/components/host-foundation-example.json').read_text())
work=pathlib.Path(tempfile.mkdtemp(prefix='clusterforge-directory-qa-')); binary=os.environ.get('ANSIBLE_PLAYBOOK') or shutil.which('ansible-playbook')
if not binary:
 raise SystemExit('Set ANSIBLE_PLAYBOOK or install ansible-playbook before running this check.')
for entry in sample:
 folder=work/entry['component']['slug']
 for f in entry['playbooks']:
  dest=folder/f['filename']; dest.parent.mkdir(parents=True,exist_ok=True); dest.write_text(f['content'])
seq=0
def run(index, phases, target, case, success=True):
 global seq
 seq+=1
 marker=str(work/case/str(index)/'baseline.json')
 cf={'inputs':{'shared_root':str(target),'prepared_root':str(target),'clusterforge_backup_marker':marker,'clusterforge_backup_metadata':{'environmentId':'isolated-local','componentId':str(index),'case':case}},'context':{'releaseId':'isolated-consumer-r1'}}
 plays=[{'hosts':'localhost','connection':'local','gather_facts':False,'vars':{'cf':cf},'tasks':[{'ansible.builtin.include_tasks':str(work/sample[index]['component']['slug']/phase)}]} for phase in phases]
 play=work/f'case-{seq}.yml';play.write_text(yaml.safe_dump(plays,sort_keys=False))
 result=subprocess.run([binary,'-i','localhost,',str(play)],capture_output=True,text=True,env={**os.environ,'ANSIBLE_LOCAL_TEMP':str(work/'ansible-tmp'),'ANSIBLE_NOCOLOR':'1'})
 (work/f'case-{seq}.log').write_text(result.stdout+result.stderr)
 if (result.returncode==0)!=success: raise RuntimeError(f'{case} {phases} unexpected result; {work}/case-{seq}.log')
 return result
phase_install=['tasks/checks/install-pre.yml','tasks/install.yml','tasks/checks/install-post.yml']
phase_rollback=['tasks/checks/rollback-pre.yml','tasks/rollback.yml','tasks/checks/rollback-post.yml']
target=pathlib.Path('/tmp/clusterforge-foundation-example-'+uuid.uuid4().hex)
run(0,phase_install,target,'new'); assert target.is_dir()
run(0,phase_install,target,'new')
run(1,phase_install,target,'new')
run(0,phase_rollback,target,'new',False); assert target.is_dir()
run(1,phase_rollback,target,'new')
run(0,phase_rollback,target,'new'); assert not target.exists()
target.mkdir(mode=0o700);(target/'business.txt').write_text('preserve user data')
run(0,phase_install,target,'existing');run(0,phase_rollback,target,'existing');assert (target/'business.txt').read_text()=='preserve user data'
(target/'business.txt').unlink();target.rmdir()
run(0,phase_install,target,'replaced');renamed=target.with_name(target.name+'-retained');target.rename(renamed);target.mkdir(mode=0o700)
run(0,phase_rollback,target,'replaced',False);assert target.exists() and renamed.exists();target.rmdir();renamed.rmdir()
target.symlink_to(work,target_is_directory=True);run(0,['tasks/checks/install-pre.yml'],target,'symlink',False);target.unlink()
print(json.dumps({'result':'passed','checks':['create and verify','repeat install','consumer lease blocks upstream rollback','reverse rollback','preserve existing nonempty directory','reject replaced inode','reject symlink'],'logs':str(work)},ensure_ascii=False))
