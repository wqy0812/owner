import { Upload as AntUpload } from 'antd';
import { useModalBusy } from './ModalBusyContext';
import { useDialogs } from './UIProvider';
import { Field } from './Field';
import { Button, Input, Select, Checkbox } from 'antd';
import { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, ClipboardCheck, FileCode2, Plus, Save, Settings2, Trash2, Upload } from 'lucide-react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { ParameterValueEditor } from './ParameterEditors';
import { EmptyState, ErrorBlock, LoadingBlock, StatusPill } from './Primitives';
import { moveAcceptanceJob, newAcceptanceJob } from '../features/scenarios/model';
import type { Component, Environment, ParameterDefinition, PlaybookWorkspaceFile, ScenarioAcceptance, ScenarioAcceptanceJob, ScenarioNode, ScenarioParameterBinding } from '../types/domain';
export function ScenarioAcceptanceEditor({ revisionId, editable, nodes, components, environments, onSaved }: {
    revisionId: string;
    editable: boolean;
    nodes: ScenarioNode[];
    components: Component[];
    environments: Environment[];
    onSaved: () => void;
}) {
    const { confirm } = useDialogs();
    const { notify, platformOptionCategories } = useApp();
    const revisionRequest = useRef(0);
    const [definition, setDefinition] = useState<ScenarioAcceptance>();
    const [jobs, setJobs] = useState<ScenarioAcceptanceJob[]>([]);
    const [parameters, setParameters] = useState<ParameterDefinition[]>([]);
    const [values, setValues] = useState<Record<string, unknown>>({});
    const [bindings, setBindings] = useState<ScenarioParameterBinding[]>([]);
    const [busy, setBusy] = useState(false);
    useModalBusy(Boolean(busy));
    const [error, setError] = useState<string>();
    const [dirty, setDirty] = useState(false);
    const [selectedFile, setSelectedFile] = useState<PlaybookWorkspaceFile>();
    const [content, setContent] = useState('');
    const [fileDirty, setFileDirty] = useState(false);
    const [newPath, setNewPath] = useState('templates/acceptance.j2');
    const hostGroups = platformOptionCategories.filter(category => category.kind === 'host_group').flatMap(category => category.options.filter(option => !option.retiredAt));
    const environmentNames = [...new Set(environments.flatMap(environment => [
            ...Object.keys(environment.currentRevision?.parameters ?? {}), ...Object.keys(environment.currentRevision?.variables ?? {}),
        ]))].sort();
    const credentialNames = [...new Set(environments.flatMap(environment => environment.currentRevision?.credentialRefs.map(ref => ref.name) ?? []))].sort();
    const releaseMap = useMemo(() => new Map(components.flatMap(component => component.releases?.map(release => [release.id, release] as const) ?? [])), [components]);
    const canEdit = editable && definition?.editable === true;
    function apply(next: ScenarioAcceptance) {
        setDefinition(next);
        setJobs(next.jobs);
        setParameters(next.parameters);
        setValues(next.values);
        setBindings(next.bindings);
        setDirty(false);
    }
    useEffect(() => {
        const controller = new AbortController();
        revisionRequest.current++;
        setBusy(false);
        setDirty(false);
        setContent('');
        setDefinition(undefined);
        setError(undefined);
        setSelectedFile(undefined);
        setFileDirty(false);
        void api.scenarioAcceptance(revisionId, controller.signal).then(next => { if (!controller.signal.aborted) apply(next); }).catch(reason => {
            if (!controller.signal.aborted)
                setError(displayError(reason));
        });
        return () => { controller.abort(); revisionRequest.current++; };
    }, [revisionId]);
    function updateJob(index: number, patch: Partial<ScenarioAcceptanceJob>) { setJobs(items => items.map((job, i) => i === index ? { ...job, ...patch } : job)); setDirty(true); }
    function updateParameter(index: number, patch: Partial<ParameterDefinition>) {
        const oldName = parameters[index].name;
        if (patch.name !== undefined && patch.name !== oldName) {
            setBindings(items => items.map(binding => binding.parameter === oldName ? { ...binding, parameter: patch.name! } : binding));
            setValues(current => {
                const next = { ...current };
                if (oldName in next) {
                    next[patch.name!] = next[oldName];
                    delete next[oldName];
                }
                return next;
            });
        }
        setParameters(items => items.map((parameter, i) => i === index ? { ...parameter, ...patch } : parameter));
        setDirty(true);
    }
    function setBinding(parameter: string, binding?: ScenarioParameterBinding) {
        setBindings(items => [...items.filter(item => item.parameter !== parameter), ...(binding ? [binding] : [])]);
        setValues(current => { const next = { ...current }; delete next[parameter]; return next; });
        setDirty(true);
    }
    async function saveDefinition() {
        if (!definition || !canEdit || busy)
            return;
        const request = revisionRequest.current;
        setBusy(true);
        try {
            const saved = await api.saveScenarioAcceptance(revisionId, { jobs: jobs.map(job => ({ ...job, requiredCredentials: job.requiredCredentials.map(name => name.trim()).filter(Boolean) })), parameters, values, bindings, expectedRevisionDigest: definition.revisionDigest });
            if (request !== revisionRequest.current) return;
            apply(saved);
            onSaved();
            notify('success', '业务验收已保存', '作业与参数变化已使旧测试证据失效。');
        }
        catch (reason) {
            if (request !== revisionRequest.current) return;
            notify('error', '保存业务验收失败', displayError(reason));
        }
        finally {
            if (request === revisionRequest.current) setBusy(false);
        }
    }
    async function openFile(path: string) {
        const request = revisionRequest.current;
        if (fileDirty && !await confirm('当前文件有未保存内容，确认放弃并打开其他文件？'))
            return;
        if (request !== revisionRequest.current) return;
        setBusy(true);
        try {
            const file = await api.scenarioAcceptanceFile(revisionId, path);
            if (request !== revisionRequest.current) return;
            setSelectedFile(file);
            setContent(file.content ?? '');
            setFileDirty(false);
        }
        catch (reason) {
            if (request !== revisionRequest.current) return;
            notify('error', '读取验收文件失败', displayError(reason));
        }
        finally {
            if (request === revisionRequest.current) setBusy(false);
        }
    }
    async function saveFile(path: string, text: string, file?: File) {
        if (!definition || dirty || !canEdit || busy)
            return;
        const request = revisionRequest.current;
        setBusy(true);
        try {
            const input = { path, expectedSha256: definition.workspace.files.find(item => item.path === path)?.sha256 ?? '', expectedTreeSha256: definition.workspace.treeSha256, expectedRevisionDigest: definition.revisionDigest };
            // An opened editor always submits the SHA it read, even if a refreshed tree
            // now contains a newer file. The server's CAS rejects stale text safely.
            if (selectedFile?.path === path)
                input.expectedSha256 = selectedFile.sha256;
            const saved = file ? await api.uploadScenarioAcceptanceFile(revisionId, file, input) : await api.saveScenarioAcceptanceFile(revisionId, { ...input, content: text });
            if (request !== revisionRequest.current) return;
            const refreshed = await api.scenarioAcceptance(revisionId);
            if (request !== revisionRequest.current) return;
            apply(refreshed);
            setSelectedFile(saved);
            setContent(saved.content ?? text);
            setFileDirty(false);
            onSaved();
            notify('success', '验收文件已保存', path);
        }
        catch (reason) {
            if (request !== revisionRequest.current) return;
            notify('error', '保存验收文件失败', displayError(reason));
        }
        finally {
            if (request === revisionRequest.current) setBusy(false);
        }
    }
    async function deleteFile() {
        const request = revisionRequest.current;
        if (!definition || !selectedFile || dirty || !canEdit || busy || !await confirm(`删除验收工作区文件 ${selectedFile.path}？`))
            return;
        if (request !== revisionRequest.current) return;
        setBusy(true);
        try {
            await api.deleteScenarioAcceptanceFile(revisionId, { path: selectedFile.path, expectedSha256: selectedFile.sha256, expectedTreeSha256: definition.workspace.treeSha256, expectedRevisionDigest: definition.revisionDigest });
            if (request !== revisionRequest.current) return;
            const refreshed = await api.scenarioAcceptance(revisionId);
            if (request !== revisionRequest.current) return;
            apply(refreshed);
            setSelectedFile(undefined);
            setContent('');
            setFileDirty(false);
            onSaved();
        }
        catch (reason) {
            if (request !== revisionRequest.current) return;
            notify('error', '删除验收文件失败', displayError(reason));
        }
        finally {
            if (request === revisionRequest.current) setBusy(false);
        }
    }
    async function newFile(path: string, initialContent = '') {
        const request = revisionRequest.current;
        if (fileDirty && !await confirm('当前文件有未保存内容，确认放弃并新建文件？')) return;
        if (request !== revisionRequest.current || !canEdit || busy || dirty) return;
        setSelectedFile({ releaseId: revisionId, path, sha256: '', sizeBytes: 0, mediaType: 'text/plain', editable: true });
        setContent(initialContent);
        setFileDirty(true);
    }
    async function retryDefinition() {
        const request = revisionRequest.current;
        setError(undefined);
        try {
            const next = await api.scenarioAcceptance(revisionId);
            if (request === revisionRequest.current) apply(next);
        } catch (reason) {
            if (request === revisionRequest.current) setError(displayError(reason));
        }
    }
    if (error)
        return <ErrorBlock message={error} onRetry={() => void retryDefinition()}/>;
    if (!definition)
        return <LoadingBlock label="正在读取业务验收…"/>;
    return <section className="scenario-acceptance panel">
    <header className="scenario-section-header"><div className="scenario-section-title"><span className="panel__icon panel__icon--cyan"><ClipboardCheck size={18} aria-hidden="true"/></span><div><h3>场景业务验收</h3><p>组件全部验证通过后，按顺序执行下列 Ansible 验收作业；任一失败立即停止。</p></div></div>{canEdit && <Button className="button button--primary" disabled={busy || !dirty} onClick={() => void saveDefinition()} htmlType={"button"} type="primary"><Save size={15}/> 保存业务验收</Button>}</header>
    {!jobs.length && <EmptyState title="尚未配置业务验收" description="至少添加一项业务验收，保存后录入其 Ansible 任务，才可完成测试与发布。"/>}
    <div className="scenario-acceptance-jobs">{jobs.map((job, index) => <article className="scenario-acceptance-job" key={job.id}>
      <header><strong className="scenario-job-title"><span>{index + 1}</span>{job.name || '未命名验收'}</strong><div className="scenario-inline-actions">{canEdit && <><Button aria-label={`上移 ${job.name}`} className="button button--quiet" disabled={busy || index === 0} onClick={() => { setJobs(moveAcceptanceJob(jobs, index, -1)); setDirty(true); }} htmlType={"button"} type="default"><ArrowUp size={15}/></Button><Button aria-label={`下移 ${job.name}`} className="button button--quiet" disabled={busy || index === jobs.length - 1} onClick={() => { setJobs(moveAcceptanceJob(jobs, index, 1)); setDirty(true); }} htmlType={"button"} type="default"><ArrowDown size={15}/></Button><Button aria-label={`删除 ${job.name}`} className="button button--danger-soft" disabled={busy} onClick={() => { setJobs(jobs.filter((_, i) => i !== index)); setDirty(true); }} htmlType={"button"} type="default" danger><Trash2 size={15}/></Button></>}</div></header>
      <fieldset disabled={!canEdit || busy} className="form-grid">
        <Field label={"验收名称"}><Input aria-label={`验收名称 ${index + 1}`} value={job.name} onChange={event => updateJob(index, { name: event.target.value })}/></Field>
        <Field label={"目标主机组"}><Select disabled={!canEdit || busy} aria-label={`验收主机组 ${index + 1}`} value={job.hostGroup} onChange={(selectedValue) => updateJob(index, { hostGroup: selectedValue })} popupMatchSelectWidth={true}><Select.Option value="">请选择</Select.Option>{hostGroups.map(group => <Select.Option key={group.id} value={group.value}>{group.label}</Select.Option>)}{job.hostGroup && !hostGroups.some(group => group.value === job.hostGroup) && <Select.Option value={job.hostGroup}>{job.hostGroup}（需复核）</Select.Option>}</Select></Field>
        <Field className="span-2" label={"验收目的与成功条件"}><Input.TextArea aria-label={`验收目的 ${index + 1}`} rows={2} value={job.purpose} onChange={event => updateJob(index, { purpose: event.target.value })}/></Field>
        <Field label={"超时（秒）"}><Input type="number" min={1} max={86400} value={job.timeoutSeconds} onChange={event => updateJob(index, { timeoutSeconds: Number(event.target.value) })}/></Field>
        <Field label={"风险等级"}><Select disabled={!canEdit || busy} value={job.riskLevel} onChange={(selectedValue) => updateJob(index, { riskLevel: selectedValue as ScenarioAcceptanceJob['riskLevel'] })} popupMatchSelectWidth={true}><Select.Option value="low">低风险</Select.Option><Select.Option value="medium">中风险</Select.Option><Select.Option value="high">高风险</Select.Option><Select.Option value="destructive">破坏性操作</Select.Option></Select></Field>
        <Field label={"所需凭据引用名称（每行一项）"} extra={<><small>只填写 CredentialRef 名称，由环境提供引用。{credentialNames.length > 0 ? `已有：${credentialNames.join('、')}` : ''}</small></>}><Input.TextArea rows={2} value={job.requiredCredentials.join('\n')} onChange={event => updateJob(index, { requiredCredentials: event.target.value.split('\n') })}/></Field>
        <div className="scenario-checkboxes"><Checkbox disabled={!canEdit || busy} checked={job.become} onChange={event => updateJob(index, { become: event.target.checked })}> 提权执行</Checkbox><Checkbox disabled={!canEdit || busy} checked={job.mayMutate} onChange={event => updateJob(index, { mayMutate: event.target.checked })}> 允许临时业务操作</Checkbox></div>
      </fieldset>
      {job.mayMutate && <p className="inline-warning">在成功路径中清理临时资源；失败后保留现场，清理失败也会使验收失败。</p>}
      <div className="scenario-inline-actions"><StatusPill status={job.playbookSha256 ? 'ready' : 'pending'}>{job.playbookSha256 ? '已录入 Ansible 任务' : '待录入 Ansible 任务'}</StatusPill><Button className="button button--secondary" disabled={busy || dirty || !job.playbook} onClick={() => {
                const path = `tasks/acceptance/${job.id}.yml`;
                if (definition.workspace.files.some(file => file.path === path))
                    void openFile(path);
                else {
                    void newFile(path, '- name: 验证业务响应\n  ansible.builtin.uri:\n    url: "{{ acceptance_url }}"\n    status_code: 200\n');
                }
            }} htmlType={"button"} type="default">编辑 Ansible 任务</Button></div>
    </article>)}</div>
    {canEdit && <Button className="button button--secondary" disabled={busy} onClick={() => { setJobs([...jobs, newAcceptanceJob(jobs.length, hostGroups[0]?.value)]); setDirty(true); }} htmlType={"button"} type="default"><Plus size={15}/> 添加验收作业</Button>}
    <section className="scenario-acceptance-parameters"><header className="scenario-section-title"><span className="panel__icon"><Settings2 size={18} aria-hidden="true"/></span><div><h3>验收参数</h3><p>录入类型化值，或绑定组件公开参数及环境字段。</p></div></header>{!parameters.length && <EmptyState title="尚未配置验收参数" description="需要传入业务地址等信息时，可添加参数并选择值的来源。"/>}{parameters.map((parameter, index) => {
            const binding = bindings.find(item => item.parameter === parameter.name);
            const sourceOptions = nodes.flatMap(node => (releaseMap.get(node.data.releaseId)?.parameters ?? []).filter(item => item.visibility === 'public' && item.type === parameter.type).map(item => ({ value: `${node.id}::${item.name}`, label: `${node.data.label} · ${item.name}` })));
            return <article key={index} className="scenario-acceptance-parameter"><fieldset disabled={!canEdit || busy} className="form-grid">
        <Field label={"参数名称"}><Input aria-label={`验收参数名称 ${index + 1}`} value={parameter.name} onChange={event => updateParameter(index, { name: event.target.value })}/></Field><Field label={"说明"}><Input value={parameter.description} onChange={event => updateParameter(index, { description: event.target.value })}/></Field>
        <Field label={"类型"}><Select disabled={!canEdit || busy} value={parameter.type} onChange={(selectedValue) => { updateParameter(index, { type: selectedValue as ParameterDefinition['type'], enum: undefined }); setBinding(parameter.name); }} popupMatchSelectWidth={true}><Select.Option value="string">文本</Select.Option><Select.Option value="boolean">开关</Select.Option><Select.Option value="integer">整数</Select.Option><Select.Option value="number">数值</Select.Option>{['array', 'object'].includes(parameter.type) && <Select.Option value={parameter.type}>{parameter.type}（受控选项）</Select.Option>}</Select></Field>
        <Field label={"值的来源"}><Select disabled={!canEdit || busy} value={binding?.source ?? 'value'} onChange={(selectedValue) => setBinding(parameter.name, selectedValue === 'value' ? undefined : { parameter: parameter.name, source: selectedValue as 'node' | 'environment', sourceParameter: '' })} popupMatchSelectWidth={true}><Select.Option value="value">场景 Owner 填写</Select.Option><Select.Option value="node">节点公开参数</Select.Option><Select.Option value="environment">环境字段</Select.Option></Select></Field>
        <Field className="span-2" label={binding ? '绑定来源' : '参数值'}>{binding?.source === 'node' ? <Select disabled={!canEdit || busy} value={binding.nodeId && binding.sourceParameter ? `${binding.nodeId}::${binding.sourceParameter}` : ''} onChange={(selectedValue) => { const [nodeId, sourceParameter] = selectedValue.split('::'); setBinding(parameter.name, { ...binding, nodeId, sourceParameter }); }} popupMatchSelectWidth={true}><Select.Option value="">请选择同类型公开参数</Select.Option>{sourceOptions.map(option => <Select.Option key={option.value} value={option.value}>{option.label}</Select.Option>)}{binding.sourceParameter && !sourceOptions.some(option => option.value === `${binding.nodeId}::${binding.sourceParameter}`) && <Select.Option value={`${binding.nodeId}::${binding.sourceParameter}`}>失效来源 · {binding.sourceParameter}</Select.Option>}</Select> : binding?.source === 'environment' ? <Select disabled={!canEdit || busy} value={binding.sourceParameter} onChange={(selectedValue) => setBinding(parameter.name, { ...binding, sourceParameter: selectedValue })} popupMatchSelectWidth={true}><Select.Option value="">请选择环境字段</Select.Option>{environmentNames.map(name => <Select.Option key={name} value={name}>{name}</Select.Option>)}{binding.sourceParameter && !environmentNames.includes(binding.sourceParameter) && <Select.Option value={binding.sourceParameter}>{binding.sourceParameter}（当前环境未提供）</Select.Option>}</Select> : <ParameterValueEditor parameter={parameter} value={values[parameter.name]} disabled={!canEdit || busy} optional={!parameter.required} onChange={value => {
                        setValues(current => {
                            const next = { ...current };
                            if (value === undefined)
                                delete next[parameter.name];
                            else
                                next[parameter.name] = value;
                            return next;
                        });
                        setDirty(true);
                    }}/>}</Field>
        <Checkbox disabled={!canEdit || busy} className="scenario-checkboxes" checked={parameter.required === true} onChange={event => updateParameter(index, { required: event.target.checked })}> 必填</Checkbox>
      </fieldset>{canEdit && <Button disabled={busy} className="button button--danger-soft" onClick={() => { setParameters(parameters.filter((_, i) => i !== index)); setBinding(parameter.name); setDirty(true); }} htmlType={"button"} type="default" danger><Trash2 size={15}/> 删除参数</Button>}</article>;
        })}{canEdit && <Button className="button button--secondary" disabled={busy} onClick={() => { setParameters([...parameters, { name: '', description: '', type: 'string', required: false, visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner' }]); setDirty(true); }} htmlType={"button"} type="default"><Plus size={15}/> 添加验收参数</Button>}</section>
    <section className="scenario-acceptance-workspace"><header className="scenario-section-title"><span className="panel__icon panel__icon--amber"><FileCode2 size={18} aria-hidden="true"/></span><div><h3>Ansible 任务与辅助文件</h3><p>入口路径由平台生成，工作区独立。请在验收 YAML 中自行采集 facts 并检查必要工具。</p></div></header>{dirty && <p className="inline-warning">请先保存作业和参数，再编辑或上传文件。</p>}
      <div className="scenario-workspace-layout"><nav aria-label="验收工作区文件">{definition.workspace.files.length ? definition.workspace.files.map(file => <button className={selectedFile?.path === file.path ? 'active' : ''} key={file.path} disabled={busy || dirty} onClick={() => void openFile(file.path)}>{file.path}</button>) : <p>工作区尚无文件。</p>}</nav><div>
        {selectedFile ? <><strong className="scenario-file-path">{selectedFile.path}</strong>{selectedFile.editable !== false ? <Input.TextArea aria-label="验收文件在线编辑器" className="code-editor" rows={17} value={content} disabled={!canEdit || busy || dirty} spellCheck={false} onChange={event => { setContent(event.target.value); setFileDirty(true); }}/> : <p>该文件不是可编辑文本，可以上传替换文件。</p>}{canEdit && <div className="scenario-inline-actions"><Button className="button button--primary" disabled={busy || dirty || !fileDirty || selectedFile.editable === false} onClick={() => void saveFile(selectedFile.path, content)} htmlType={"button"} type="primary"><Save size={15}/> 保存验收文件</Button><AntUpload showUploadList={false}  disabled={busy || dirty} beforeUpload={selectedUpload => {
                    const file = selectedUpload;
                    if (file)
                        void saveFile(selectedFile.path, '', file);

                ;return false;}}><Button disabled={busy || dirty}><Upload size={15}/>上传替换</Button></AntUpload><Button className="button button--danger-soft" disabled={busy || dirty || !selectedFile.sha256} onClick={() => void deleteFile()} htmlType={"button"} type="default" danger><Trash2 size={15}/> 删除文件</Button></div>}</> : <EmptyState title="选择一个验收文件" description="从作业打开入口任务，或创建、上传辅助文件。"/>}
      </div></div>{canEdit && <div className="scenario-workspace-create"><Field label={"辅助文件相对路径"}><Input aria-label="验收辅助文件路径" value={newPath} disabled={busy || dirty} onChange={event => setNewPath(event.target.value)} placeholder="templates/check.j2"/></Field><Button className="button button--secondary" disabled={busy || dirty || !newPath.trim()} onClick={() => void newFile(newPath.trim())} htmlType={"button"} type="default">新建文本文件</Button><AntUpload showUploadList={false}  disabled={busy || dirty || !newPath.trim()} beforeUpload={selectedUpload => {
                const file = selectedUpload;
                if (file)
                    void saveFile(newPath.trim(), '', file);

            ;return false;}}><Button disabled={busy || dirty || !newPath.trim()}><Upload size={15}/>上传辅助文件</Button></AntUpload></div>}
    </section>
  </section>;
}
