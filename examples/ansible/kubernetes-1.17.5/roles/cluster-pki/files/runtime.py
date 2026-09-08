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

COMPONENT='cluster-pki';PATHS=['/etc/kubernetes/pki', '/etc/kubernetes/admin.conf', '/etc/kubernetes/kubelet.conf', '/etc/kubernetes/controller-manager.conf', '/etc/kubernetes/scheduler.conf'];SERVICES=[]
def precheck():
    if exists('/etc/kubernetes/pki/ca.key') and not MARKER.exists():raise RuntimeError('Refuse to replace existing trust material')
    assert I['pki_dir']=='/etc/kubernetes/pki'
def cert(root,name,cn,org=None,sans=None,ca='ca',client=False):
    stem=root/name;stem.parent.mkdir(parents=True,exist_ok=True)
    if pathlib.Path(str(stem)+'.crt').exists():return
    subj='/CN='+cn+('/O='+org if org else '')
    run(['openssl','genrsa','-out',str(stem)+'.key','2048'])
    run(['openssl','req','-new','-key',str(stem)+'.key','-subj',subj,'-out',str(stem)+'.csr'])
    ext='basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage='+('clientAuth' if client else 'serverAuth,clientAuth')+'\n'
    if sans:ext+='subjectAltName='+','.join(sans)+'\n'
    write(str(stem)+'.ext',ext)
    run(['openssl','x509','-req','-in',str(stem)+'.csr','-CA',str(root/ca)+'.crt','-CAkey',str(root/ca)+'.key','-CAcreateserial','-out',str(stem)+'.crt','-days','365','-sha256','-extfile',str(stem)+'.ext'])
    os.chmod(str(stem)+'.key',0o600)
def kubeconfig(root,certname,user):
    enc=lambda p:base64.b64encode(p.read_bytes()).decode()
    return {'apiVersion':'v1','kind':'Config','clusters':[{'name':'k8s1175','cluster':{'server':'https://'+I['control_plane_endpoint'],'certificate-authority-data':enc(root/'ca.crt')}}],'users':[{'name':user,'user':{'client-certificate-data':enc(root/(certname+'.crt')),'client-key-data':enc(root/(certname+'.key'))}}],'contexts':[{'name':'default','context':{'cluster':'k8s1175','user':user}}],'current-context':'default'}
def install():
    if NODE!=PRIMARY:return
    root=PKI/'authority';root.mkdir(parents=True,exist_ok=True);os.chmod(str(root),0o700)
    for name in ['ca','etcd-ca','front-proxy-ca']:
        if not (root/(name+'.crt')).exists():run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/(name+'.key')),'-out',str(root/(name+'.crt')),'-sha256','-days','3650','-subj','/CN=clusterforge-1175-'+name])
        os.chmod(str(root/(name+'.key')),0o600)
    if not (root/'sa.key').exists():run(['openssl','genrsa','-out',str(root/'sa.key'),'2048'])
    run(['openssl','rsa','-in',str(root/'sa.key'),'-pubout','-out',str(root/'sa.pub')]);os.chmod(str(root/'sa.key'),0o600)
    cert(root,'admin','clusterforge-admin','system:masters',client=True)
    cert(root,'controller-manager','system:kube-controller-manager',client=True)
    cert(root,'scheduler','system:kube-scheduler',client=True)
    cert(root,'apiserver-kubelet-client','kube-apiserver-kubelet-client','system:masters',client=True)
    cert(root,'apiserver-etcd-client','kube-apiserver-etcd-client',ca='etcd-ca',client=True)
    cert(root,'front-proxy-client','front-proxy-client',ca='front-proxy-ca',client=True)
    for node,ip in HOSTS.items():
        cert(root,node+'/kubelet','system:node:'+node,'system:nodes',client=True)
        bundle={'pki/ca.crt':(root/'ca.crt').read_text(),'pki/kubelet-client.crt':(root/node/'kubelet.crt').read_text(),'pki/kubelet-client.key':(root/node/'kubelet.key').read_text(),'kubelet.conf':json.dumps(kubeconfig(root,node+'/kubelet',node))}
        if node in MASTERS:
            cert(root,node+'/apiserver','kube-apiserver',sans=['DNS:kubernetes','DNS:kubernetes.default','DNS:kubernetes.default.svc','DNS:kubernetes.default.svc.cluster.local','DNS:localhost','DNS:'+node,'IP:127.0.0.1','IP:10.96.0.1']+['IP:'+HOSTS[m] for m in MASTERS])
            cert(root,node+'/etcd-server',node,sans=['DNS:'+node,'DNS:localhost','IP:127.0.0.1','IP:'+ip],ca='etcd-ca')
            cert(root,node+'/etcd-peer',node,sans=['DNS:'+node,'IP:'+ip],ca='etcd-ca')
            for dest,src in {'ca.key':'ca.key','sa.key':'sa.key','sa.pub':'sa.pub','front-proxy-ca.crt':'front-proxy-ca.crt','front-proxy-ca.key':'front-proxy-ca.key','front-proxy-client.crt':'front-proxy-client.crt','front-proxy-client.key':'front-proxy-client.key','apiserver.crt':node+'/apiserver.crt','apiserver.key':node+'/apiserver.key','apiserver-kubelet-client.crt':'apiserver-kubelet-client.crt','apiserver-kubelet-client.key':'apiserver-kubelet-client.key','apiserver-etcd-client.crt':'apiserver-etcd-client.crt','apiserver-etcd-client.key':'apiserver-etcd-client.key','etcd/ca.crt':'etcd-ca.crt','etcd/ca.key':'etcd-ca.key','etcd/server.crt':node+'/etcd-server.crt','etcd/server.key':node+'/etcd-server.key','etcd/peer.crt':node+'/etcd-peer.crt','etcd/peer.key':node+'/etcd-peer.key'}.items():bundle['pki/'+dest]=(root/src).read_text()
            for file,user in [('admin','admin'),('controller-manager','controller-manager'),('scheduler','scheduler')]:bundle[file+'.conf']=json.dumps(kubeconfig(root,user,user))
        write(PKI/'issued'/(node+'.json'),bundle)
def postcheck():
    for name in ['ca.crt','kubelet-client.crt','kubelet-client.key']:assert (PKI/name).exists(),name
    run(['openssl','verify','-CAfile',str(PKI/'ca.crt'),str(PKI/'kubelet-client.crt')])
    assert stat.S_IMODE((PKI/'kubelet-client.key').stat().st_mode)==0o600
    assert json.loads(pathlib.Path('/etc/kubernetes/kubelet.conf').read_text())['clusters'][0]['cluster']['server']=='https://'+I['control_plane_endpoint']
    if IS_MASTER:
        run(['openssl','verify','-CAfile',str(PKI/'ca.crt'),str(PKI/'apiserver.crt')])
        run(['openssl','verify','-CAfile',str(PKI/'etcd/ca.crt'),str(PKI/'etcd/server.crt')])
def rollback_precheck():
    assert not list(pathlib.Path('/etc/kubernetes/manifests').glob('*.yaml')),'Control-plane manifests must be removed before trust rollback'


if __name__ == "__main__":
    dispatch()
