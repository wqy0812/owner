import io,tarfile,urllib.request
import base64,hashlib,json,os,pathlib,re,shutil,socket,stat,subprocess,sys,time

I=json.loads(os.environ.get('CF_INPUTS','{}'))
NODE=os.environ['CF_NODE']; IP=os.environ['CF_IP']
NAMES=json.loads(os.environ['CF_NAMES']); IPS=json.loads(os.environ['CF_IPS'])
MASTERS=json.loads(os.environ['CF_MASTERS']); PRIMARY=MASTERS[0]
HOSTS=dict(zip(NAMES,IPS)); IS_MASTER=NODE in MASTERS
MODE=sys.argv[1]
BACKUP=pathlib.Path(I['clusterforge_backup_ref'])
MARKER=pathlib.Path(I['clusterforge_backup_marker'])
METADATA=I['clusterforge_backup_metadata']
PKI=pathlib.Path(I.get('pki_dir','/etc/kubernetes/pki'))
ADMIN=I.get('admin_kubeconfig_path','/etc/kubernetes/admin.conf')

def run(args,input=None,ok=(0,)):
    p=subprocess.run(args,input=input,stdout=subprocess.PIPE,stderr=subprocess.PIPE,universal_newlines=True,timeout=180)
    if p.returncode not in ok:
        raise RuntimeError('Command '+str(args[:2])+' failed: '+p.stderr[-2200:])
    return p.stdout.strip()

def wait_for(fn,seconds=180):
    deadline=time.time()+seconds;last=''
    while time.time()<deadline:
        try:
            value=fn()
            if value is not False:return value
        except Exception as e:last=str(e)
        time.sleep(2)
    raise RuntimeError('Timed out: '+last)

def write(path,content,mode=0o600):
    p=pathlib.Path(path);p.parent.mkdir(parents=True,exist_ok=True)
    p.write_text(content if isinstance(content,str) else json.dumps(content,indent=2)+'\n');os.chmod(str(p),mode)

def exists(path):return pathlib.Path(path).exists() or pathlib.Path(path).is_symlink()

def mounted_under(path):
    base=str(path).rstrip('/')
    return any((p.split()[4].replace('\\040',' ')==base or p.split()[4].replace('\\040',' ').startswith(base+'/')) for p in pathlib.Path('/proc/self/mountinfo').read_text().splitlines())

def remove(path):
    p=pathlib.Path(path)
    if not exists(p):return
    if mounted_under(p):raise RuntimeError('Refuse to remove mounted resource '+str(p))
    if p.is_dir() and not p.is_symlink():shutil.rmtree(str(p))
    else:p.unlink()

def copy_path(src,dst):
    s,d=pathlib.Path(src),pathlib.Path(dst);d.parent.mkdir(parents=True,exist_ok=True)
    if s.is_symlink():d.symlink_to(os.readlink(str(s)))
    elif s.is_dir():shutil.copytree(str(s),str(d),symlinks=True)
    else:shutil.copy2(str(s),str(d))
    def owner(a,b):
        st=a.lstat();os.chown(str(b),st.st_uid,st.st_gid,follow_symlinks=False)
    owner(s,d)
    if s.is_dir() and not s.is_symlink():
        for p in s.rglob('*'):owner(p,d/p.relative_to(s))

def fingerprint(path):
    p=pathlib.Path(path)
    if not exists(p):return None
    def one(f):
        s=f.lstat();data={'mode':stat.S_IMODE(s.st_mode),'uid':s.st_uid,'gid':s.st_gid}
        if f.is_symlink():data['link']=os.readlink(str(f))
        elif f.is_file():data['sha256']=hashlib.sha256(f.read_bytes()).hexdigest()
        elif f.is_dir():data['dir']=True
        else:raise RuntimeError('Unsupported baseline object '+str(f))
        return data
    out={'.':one(p)}
    if p.is_dir() and not p.is_symlink():
        for f in sorted(p.rglob('*')):out[str(f.relative_to(p))]=one(f)
    return out

def service_state(name):
    return {'active':run(['systemctl','is-active',name],ok=(0,3,4)), 'enabled':run(['systemctl','is-enabled',name],ok=(0,1,3,4))}

def baseline_read():
    b=json.loads(MARKER.read_text())
    assert b['metadata']==METADATA,'Recovery identity changed'
    assert b['component']==COMPONENT,'Recovery component changed'
    return b

def capture():
    if MARKER.exists():baseline_read();return
    assert str(BACKUP).startswith('/'),'Absolute recovery path required'
    BACKUP.mkdir(parents=True,exist_ok=True);os.chmod(str(BACKUP),0o700)
    parents=set()
    for path in PATHS:
        for parent in pathlib.Path(path).parents:
            if str(parent)!='/' and not parent.exists():parents.add(str(parent))
    state={'created_parent_dirs':sorted(parents,key=lambda p:-len(p)), 'component':COMPONENT,'metadata':METADATA,'paths':{},'services':{},'extra':capture_extra()}
    for idx,path in enumerate(PATHS):
        f=fingerprint(path);state['paths'][path]={'fingerprint':f,'slot':str(idx)}
        if f is not None:copy_path(path,BACKUP/'resources'/str(idx))
    for name in SERVICES:state['services'][name]=service_state(name)
    write(MARKER,state)

def restore():
    b=baseline_read()
    for name in SERVICES:run(['systemctl','stop',name])
    for path,entry in b['paths'].items():
        remove(path)
        if entry['fingerprint'] is not None:copy_path(BACKUP/'resources'/entry['slot'],path)
    restore_extra(b['extra'])
    for path in b.get('created_parent_dirs',[]):
        p=pathlib.Path(path)
        if p.is_dir() and not p.is_symlink() and not any(p.iterdir()):p.rmdir()
    run(['systemctl','daemon-reload'])
    for name,s in b['services'].items():
        if s['enabled']=='enabled':run(['systemctl','enable',name])
        elif s['enabled']=='disabled':run(['systemctl','disable',name])
        if s['active']=='active':run(['systemctl','start',name])

def restored():
    b=baseline_read()
    for path,entry in b['paths'].items():assert fingerprint(path)==entry['fingerprint'],'Restoration differs: '+path
    for name,expected in b['services'].items():assert service_state(name)==expected,'Service restoration differs: '+name
    verify_extra(b['extra'])

def capture_extra():return {}
def restore_extra(state):pass
def verify_extra(state):pass

def kubectl(args,obj=None,ok=(0,)):
    return run(['kubectl','--kubeconfig='+ADMIN]+args,json.dumps(obj) if obj is not None else None,ok)

def get_object(kind,name,ns=None):
    cmd=['get',kind,name,'--ignore-not-found','-o','json']
    if ns:cmd+=['-n',ns]
    data=kubectl(cmd)
    return json.loads(data) if data else None

def apply_objects(objects):
    receipt=BACKUP/'api-resources.json';entries=json.loads(receipt.read_text()) if receipt.exists() else []
    known={(x['kind'],x['name'],x.get('namespace')):x for x in entries}
    for obj in objects:
        m=obj['metadata'];key=(obj['kind'],m['name'],m.get('namespace'));before=get_object(*key)
        if before:
            assert key in known,'Refuse to adopt an existing API resource: '+m['name']
            expected=known[key]['uid']
            assert before['metadata']['uid']==expected if expected else before['metadata'].get('annotations',{}).get('clusterforge.io/owner')==str(METADATA),'Resource ownership changed'
        if key not in known:
            entry={'kind':key[0],'name':key[1],'namespace':key[2],'uid':None};entries.append(entry);known[key]=entry
            write(receipt,entries)
        m.setdefault('annotations',{})['clusterforge.io/owner']=str(METADATA)
        kubectl(['apply','-f','-'],obj)
        after=get_object(*key)
        known[key]['uid']=after['metadata']['uid'];write(receipt,entries)

def delete_objects():
    receipt=BACKUP/'api-resources.json'
    if not receipt.exists():return
    entries=json.loads(receipt.read_text())
    for e in entries:
        obj=get_object(e['kind'],e['name'],e['namespace'])
        if obj:
            assert (obj['metadata']['uid']==e['uid']) if e['uid'] else obj['metadata'].get('annotations',{}).get('clusterforge.io/owner')==str(METADATA),'Resource was replaced: '+e['name']
    for e in reversed(entries):
        cmd=['delete',e['kind'],e['name'],'--ignore-not-found','--wait=false']
        if e['namespace']:cmd+=['-n',e['namespace']]
        kubectl(cmd)
    for e in entries:wait_for(lambda: get_object(e['kind'],e['name'],e['namespace']) is None)

def objects_absent():
    r=BACKUP/'api-resources.json'
    if r.exists():
        for e in json.loads(r.read_text()):assert get_object(e['kind'],e['name'],e['namespace']) is None,'API resource remains'

def image(name):
    ref=I[name+'_image_ref'];digest=I[name+'_image_digest']
    if '@' in ref:return ref
    return ref+'@'+digest

def containers(name):
    ids=run(['docker','ps','-q','--filter','label=io.kubernetes.container.name='+name]).split()
    return [json.loads(run(['docker','inspect',x]))[0] for x in ids]

def check_container(name):
    result=containers(name)
    return bool(result) and all(c['State']['Running'] for c in result)

def static_pod(name,args,volumes,ports=None):
    mounts=[{'name':'v'+str(i),'mountPath':p,'readOnly':ro} for i,(p,ro) in enumerate(volumes)]
    spec={'hostNetwork':True,'priorityClassName':'system-node-critical','containers':[{'name':name,'image':image(name.replace('-','_')),'imagePullPolicy':'IfNotPresent','command':args,'volumeMounts':mounts,'resources':{'requests':{'cpu':'100m'}}}], 'volumes':[{'name':'v'+str(i),'hostPath':{'path':p,'type':'Directory' if pathlib.Path(p).is_dir() else 'File'}} for i,(p,ro) in enumerate(volumes)]}
    return {'apiVersion':'v1','kind':'Pod','metadata':{'name':name,'namespace':'kube-system','labels':{'component':name,'tier':'control-plane'}},'spec':spec}

def filtered_netfilter(saved,prefixes):
        lines=saved.splitlines()
        owned={line.split()[0][1:] for line in lines if line.startswith(':') and any(line[1:].startswith(p) for p in prefixes)}
        filtered=[]
        for line in lines:
            words=line.split()
            if line.startswith(':') and words[0][1:] in owned:continue
            if line.startswith('-A ') and (words[1] in owned or any(words[i+1] in owned for i,x in enumerate(words[:-1]) if x in ['-j','-g'])):continue
            if any(p in line for p in ['flanneld masq']) and 'FLANNEL-' in prefixes:continue
            filtered.append(line)
        return '\n'.join(filtered)+'\n'

def cleanup_netfilter(prefixes):
    for binary in ['iptables','ip6tables']:
        run([binary+'-restore'],input=filtered_netfilter(run([binary+'-save']),prefixes))

def network_snapshot():return {b:run([b+'-save']) for b in ['iptables','ip6tables']}
def normalize_rules(s):return '\n'.join(re.sub(r'\[\d+:\d+\]','[0:0]',x) for x in s.splitlines() if not x.startswith('#'))
def network_restore(original,prefixes):
    # Refuse to replace unrelated firewall changes made since this component started.
    for b,saved in original.items():
        current=run([b+'-save'])
        assert normalize_rules(filtered_netfilter(current,prefixes))==normalize_rules(filtered_netfilter(saved,prefixes)),'Unrelated firewall rules changed; preserve for review: '+b
    for b,saved in original.items():run([b+'-restore'],input=saved+'\n')
def network_restored(original):
    for b,saved in original.items():assert normalize_rules(run([b+'-save']))==normalize_rules(saved),'Network baseline differs: '+b

def basic_precheck():
    assert os.geteuid()==0,'Root execution required'
    assert NODE in HOSTS and HOSTS[NODE]==IP,'Unexpected inventory identity'
    assert len(NAMES)==6 and len(MASTERS)==3,'Six nodes and three control planes required'
    assert 'VERSION_ID="18.04"' in pathlib.Path('/etc/os-release').read_text(),'Ubuntu 18.04 required'

def dispatch():
    basic_precheck()
    if MODE=='pre':precheck()
    elif MODE=='capture':capture()
    elif MODE=='install':capture();install()
    elif MODE=='post':postcheck()
    elif MODE=='rollback-pre':baseline_read();rollback_precheck()
    elif MODE=='rollback':baseline_read();rollback_precheck();rollback();restore()
    elif MODE=='rollback-post':restored();rollback_postcheck()
    else:raise RuntimeError('Unsupported mode')
    print(json.dumps({'component':COMPONENT,'phase':MODE,'node':NODE,'status':'passed'}))

def precheck():pass
def rollback_precheck():pass
def rollback():pass
def rollback_postcheck():pass

COMPONENT='kubernetes-distribution';PATHS=['/opt/cni/bin/bridge', '/opt/cni/bin/host-local', '/opt/cni/bin/loopback', '/opt/cni/bin/portmap', '/opt/cni/bin/bandwidth', '/opt/cni/bin/dhcp', '/opt/cni/bin/firewall', '/opt/cni/bin/host-device', '/opt/cni/bin/ipvlan', '/opt/cni/bin/macvlan', '/opt/cni/bin/ptp', '/opt/cni/bin/sbr', '/opt/cni/bin/static', '/opt/cni/bin/tuning', '/opt/cni/bin/vlan'];SERVICES=[]
def versions():
    assert run(['kubeadm','version','-o','short'])=='v'+I['kubernetes_version']
    assert run(['kubelet','--version'])=='Kubernetes v'+I['kubernetes_version']
    assert json.loads(run(['kubectl','version','--client','-o','json']))['clientVersion']['gitVersion']=='v'+I['kubernetes_version']
    for name in ['kubeadm','kubelet','kubectl']:
        p=pathlib.Path('/usr/bin')/name;assert p.is_file() and os.access(str(p),os.X_OK)
def capture_extra():return {name:hashlib.sha256((pathlib.Path('/usr/bin')/name).read_bytes()).hexdigest() for name in ['kubeadm','kubelet','kubectl']}
def verify_extra(s):assert capture_extra()==s,'Pre-existing distribution binary changed'
def precheck():versions()
def install_base_plugins():
    with urllib.request.urlopen(I['cni_plugins_url'],timeout=120) as response:payload=response.read()
    assert hashlib.sha256(payload).hexdigest()==I['cni_plugins_sha256'],'CNI artifact checksum mismatch'
    target=pathlib.Path('/opt/cni/bin');target.mkdir(parents=True,exist_ok=True)
    installed={}
    with tarfile.open(fileobj=io.BytesIO(payload),mode='r:gz') as archive:
        for member in archive.getmembers():
            if member.isdir():continue
            assert member.isfile() and pathlib.PurePosixPath(member.name).name in ['bridge','host-local','loopback','portmap','bandwidth','dhcp','firewall','flannel','host-device','ipvlan','macvlan','ptp','sbr','static','tuning','vlan'],'Unexpected CNI archive member'
            name=pathlib.PurePosixPath(member.name).name
            # The Flannel image supplies its version-specific plugin; install its supporting plugins from the verified archive.
            if name=='flannel':continue
            p=target/name
            p.write_bytes(archive.extractfile(member).read());p.chmod(0o755)
            installed[name]=fingerprint(p)
    write(BACKUP/'cni-installed.json',installed)
def verify_base_plugins(target):
    for name,expected in json.loads((BACKUP/'cni-installed.json').read_text()).items():
        assert fingerprint(pathlib.Path(target)/name)==expected,'Base CNI plugin changed: '+name
def install():versions();install_base_plugins()
def postcheck():versions();verify_extra(baseline_read()['extra']);verify_base_plugins('/opt/cni/bin')

if __name__ == "__main__":
    dispatch()
