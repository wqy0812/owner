"""Scenario acceptance: fail in place; delete owned probes only after all checks pass."""
import base64,json,os,pathlib,secrets,subprocess,time
I=json.loads(os.environ['CF_INPUTS']);NAMES=json.loads(os.environ['CF_NAMES']);IPS=json.loads(os.environ['CF_IPS']);MASTERS=json.loads(os.environ['CF_MASTERS'])
ADMIN=I['admin_kubeconfig_path'];HOSTS=dict(zip(NAMES,IPS));NS='cf-k8s1175-acceptance-'+secrets.token_hex(5);OWNER=secrets.token_hex(16)
HTTP_SERVER='cat > /tmp/probe-http <<\'HTTP\'\n#!/bin/sh\nwhile IFS= read -r line; do\n  [ "$line" = "$(printf \'\\r\')" ] && break\ndone\nprintf \'HTTP/1.1 200 OK\\r\\nContent-Length: %s\\r\\nConnection: close\\r\\n\\r\\n%s\' "${#NODE_NAME}" "$NODE_NAME"\nHTTP\nchmod 700 /tmp/probe-http\nexec nc -lk -p 8080 -e /tmp/probe-http'
EXPECTED_FLANNEL='sha256:b2bba065c46f3a54db41cd5181b87baa0fca64eda8b511838cdc147dfc59e76d'
def command(args,obj=None):
    p=subprocess.run(args,input=json.dumps(obj) if obj is not None else None,stdout=subprocess.PIPE,stderr=subprocess.PIPE,universal_newlines=True,timeout=90)
    if p.returncode:raise RuntimeError('Acceptance command failed: '+str(args[:2])+' '+p.stderr[-1500:])
    return p.stdout.strip()
def k(args,obj=None):return command(['kubectl','--kubeconfig='+ADMIN]+args,obj)
def get(kind,name=None,ns=None):return json.loads(k(['get',kind]+([name] if name else [])+(['-n',ns] if ns else [])+['-o','json']))
def wait(fn,seconds=240):
    deadline=time.time()+seconds;last=''
    while time.time()<deadline:
        try:
            if fn():return
        except Exception as error:last=str(error)
        time.sleep(3)
    raise RuntimeError('Acceptance condition timed out: '+last)
def announce(check,**facts):print(json.dumps(dict(check=check,status='passed',**facts)),flush=True)
def nodes_ready():
    nodes=get('nodes')['items']
    return set(n['metadata']['name'] for n in nodes)==set(NAMES) and all(n['status']['nodeInfo']['kubeletVersion']=='v1.17.5' and any(c['type']=='Ready' and c['status']=='True' for c in n['status']['conditions']) for n in nodes)
wait(nodes_ready);announce('six_nodes_ready',version='v1.17.5',nodes=NAMES)
for name in MASTERS:
    assert k(['--server=https://'+HOSTS[name]+':6443','get','--raw=/healthz'])=='ok'
    assert json.loads(k(['--server=https://'+HOSTS[name]+':6443','get','--raw=/version']))['gitVersion']=='v1.17.5'
announce('three_api_servers',masters=MASTERS)
etcd=command(['docker','ps','-q','--filter','label=io.kubernetes.container.name=etcd']).split();assert len(etcd)==1
ec=['docker','exec','-e','ETCDCTL_API=3',etcd[0],'etcdctl','--endpoints='+','.join('https://'+HOSTS[n]+':2379' for n in MASTERS),'--cacert=/etc/kubernetes/pki/etcd/ca.crt','--cert=/etc/kubernetes/pki/etcd/server.crt','--key=/etc/kubernetes/pki/etcd/server.key']
health=json.loads(command(ec+['endpoint','health','-w=json']));assert len(health)==3 and all(x['health'] for x in health)
members=json.loads(command(ec+['member','list','-w=json']))['members'];assert len(members)==3
announce('etcd_quorum',members=3)
for component in ['kube-apiserver','kube-controller-manager','kube-scheduler','etcd']:
    def mirrors_ready():
        pods=get('pods',ns='kube-system')['items']
        selected=[p for p in pods if p['metadata'].get('labels',{}).get('component')==component]
        return len(selected)==3 and {p['spec'].get('nodeName') for p in selected}==set(MASTERS) and all(p['status'].get('phase')=='Running' and any(c['type']=='Ready' and c['status']=='True' for c in p['status'].get('conditions',[])) for p in selected)
    wait(mirrors_ready,240)
for ns,name in [('kube-system','clusterforge-kube-proxy'),('kube-flannel','clusterforge-flannel')]:
    ds=get('daemonset',name,ns);assert ds['status']['desiredNumberScheduled']==6 and ds['status']['numberReady']==6,name
assert get('deployment','clusterforge-coredns','kube-system')['status'].get('readyReplicas')==2
probe_image=get('daemonset','clusterforge-flannel','kube-flannel')['spec']['template']['spec']['containers'][0]['image'];assert probe_image.endswith('@'+EXPECTED_FLANNEL)
announce('control_plane_and_addons')
namespace={'apiVersion':'v1','kind':'Namespace','metadata':{'name':NS,'annotations':{'clusterforge.io/acceptance-owner':OWNER}}}
k(['create','-f','-'],namespace);namespace_uid=get('namespace',NS)['metadata']['uid']
# This receipt makes an interrupted acceptance discoverable without exposing test Secret data.
print(json.dumps({'probeNamespace':NS,'namespaceUid':namespace_uid,'state':'created'}),flush=True)
try:
    for node in NAMES:
        pod={'apiVersion':'v1','kind':'Pod','metadata':{'name':'probe-'+node,'namespace':NS,'labels':{'app':'network-probe'}},'spec':{'nodeName':node,'restartPolicy':'Never','tolerations':[{'operator':'Exists'}],'containers':[{'name':'probe','image':probe_image,'imagePullPolicy':'IfNotPresent','command':['/bin/sh','-ec',HTTP_SERVER],'env':[{'name':'NODE_NAME','value':node}],'readinessProbe':{'httpGet':{'path':'/','port':8080},'initialDelaySeconds':1,'periodSeconds':2}}]}}
        k(['create','-f','-'],pod)
    service={'apiVersion':'v1','kind':'Service','metadata':{'name':'network-probe','namespace':NS},'spec':{'selector':{'app':'network-probe'},'ports':[{'port':8080,'targetPort':8080}]}}
    k(['create','-f','-'],service)
    wait(lambda:len(get('pods',ns=NS)['items'])==6 and all(any(c['type']=='Ready' and c['status']=='True' for c in p['status'].get('conditions',[])) for p in get('pods',ns=NS)['items']))
    pods=get('pods',ns=NS)['items'];addresses={p['spec']['nodeName']:p['status']['podIP'] for p in pods}
    for source in NAMES:
        for target,address in addresses.items():
            body=k(['exec','-n',NS,'probe-'+source,'--','wget','-qO-','-T','8','http://'+address+':8080/']);assert body==target
        answer=k(['exec','-n',NS,'probe-'+source,'--','wget','-qO-','-T','8','http://network-probe.'+NS+'.svc.cluster.local:8080/']);assert answer in NAMES
        k(['exec','-n',NS,'probe-'+source,'--','nslookup','kubernetes.default.svc.cluster.local'])
    announce('cross_node_http_service_and_dns',sourceNodes=6,podPairs=36)
    value=secrets.token_bytes(32);encoded=base64.b64encode(value).decode()
    k(['create','-f','-'],{'apiVersion':'v1','kind':'Secret','metadata':{'name':'encryption-probe','namespace':NS},'type':'Opaque','data':{'probe':encoded}})
    assert get('secret','encryption-probe',NS)['data']['probe']==encoded
    raw=json.loads(command(ec+['get','/registry/secrets/'+NS+'/encryption-probe','-w=json']))
    assert len(raw['kvs'])==1
    ciphertext=base64.b64decode(raw['kvs'][0]['value'])
    assert ciphertext.startswith(b'k8s:enc:aescbc:v1:clusterforge-key-1:') and value not in ciphertext and encoded.encode() not in ciphertext
    announce('secret_encryption_at_rest',provider='aescbc',apiRoundTrip=True)
except Exception:
    print(json.dumps({'probeNamespace':NS,'state':'retained_for_failure_investigation'}),flush=True)
    raise
current=get('namespace',NS);assert current['metadata']['uid']==namespace_uid and current['metadata']['annotations']['clusterforge.io/acceptance-owner']==OWNER
k(['delete','namespace',NS,'--wait=false'])
wait(lambda:not k(['get','namespace',NS,'--ignore-not-found','-o','name']))
announce('acceptance_probes_cleaned',namespace=NS)
