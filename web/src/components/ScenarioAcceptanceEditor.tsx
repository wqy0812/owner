import { LegacyYamlNotice } from './LegacyYamlNotice';
import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, ClipboardCheck, FileCode2, Plus, Save, Settings2, Trash2, Upload } from 'lucide-react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { ParameterValueEditor } from './ParameterEditors';
import { EmptyState, ErrorBlock, LoadingBlock, StatusPill } from './Primitives';
import { moveAcceptanceJob, newAcceptanceJob } from '../features/scenarios/model';
import type { Component, Environment, ParameterDefinition, PlaybookWorkspaceFile, ScenarioAcceptance, ScenarioAcceptanceJob, ScenarioNode, ScenarioParameterBinding } from '../types/domain';

export function ScenarioAcceptanceEditor({ revisionId, editable, nodes, components, environments, onSaved }: {
  revisionId: string; editable: boolean; nodes: ScenarioNode[]; components: Component[]; environments: Environment[]; onSaved: () => void;
}) {
  const { notify, platformOptionCategories } = useApp();
  const [definition, setDefinition] = useState<ScenarioAcceptance>();
  const [jobs, setJobs] = useState<ScenarioAcceptanceJob[]>([]);
  const [parameters, setParameters] = useState<ParameterDefinition[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [bindings, setBindings] = useState<ScenarioParameterBinding[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [dirty, setDirty] = useState(false);
  const [selectedFile, setSelectedFile] = useState<PlaybookWorkspaceFile>();
  const [content, setContent] = useState('');
  const [fileDirty, setFileDirty] = useState(false);
  const [confirmYamlMigration, setConfirmYamlMigration] = useState(false);
  const selectedJob = jobs.find(job => selectedFile?.path === `tasks/acceptance/${job.id}.yml`);
  useEffect(() => setConfirmYamlMigration(false), [selectedFile?.path, revisionId]);
  const [newPath, setNewPath] = useState('templates/acceptance.j2');
  const hostGroups = platformOptionCategories.filter(category => category.kind === 'host_group').flatMap(category => category.options.filter(option => !option.retiredAt));
  const environmentNames = [...new Set(environments.flatMap(environment => [
    ...Object.keys(environment.currentRevision?.parameters ?? {}), ...Object.keys(environment.currentRevision?.variables ?? {}),
  ]))].sort();
  const credentialNames = [...new Set(environments.flatMap(environment => environment.currentRevision?.credentialRefs.map(ref => ref.name) ?? []))].sort();
  const releaseMap = useMemo(() => new Map(components.flatMap(component => component.releases?.map(release => [release.id, release] as const) ?? [])), [components]);
  const canEdit = editable && definition?.editable === true;
  function apply(next: ScenarioAcceptance) {
    setDefinition(next); setJobs(next.jobs); setParameters(next.parameters); setValues(next.values); setBindings(next.bindings); setDirty(false);
  }
  useEffect(() => {
    const controller = new AbortController(); setDefinition(undefined); setError(undefined); setSelectedFile(undefined); setFileDirty(false);
    void api.scenarioAcceptance(revisionId, controller.signal).then(apply).catch(reason => { if (!controller.signal.aborted) setError(displayError(reason)); });
    return () => controller.abort();
  }, [revisionId]);
  function updateJob(index: number, patch: Partial<ScenarioAcceptanceJob>) { setJobs(items => items.map((job, i) => i === index ? { ...job, ...patch } : job)); setDirty(true); }
  function updateParameter(index: number, patch: Partial<ParameterDefinition>) {
    const oldName = parameters[index].name;
    if (patch.name !== undefined && patch.name !== oldName) {
      setBindings(items => items.map(binding => binding.parameter === oldName ? { ...binding, parameter: patch.name! } : binding));
      setValues(current => { const next = { ...current }; if (oldName in next) { next[patch.name!] = next[oldName]; delete next[oldName]; } return next; });
    }
    setParameters(items => items.map((parameter, i) => i === index ? { ...parameter, ...patch } : parameter)); setDirty(true);
  }
  function setBinding(parameter: string, binding?: ScenarioParameterBinding) {
    setBindings(items => [...items.filter(item => item.parameter !== parameter), ...(binding ? [binding] : [])]);
    setValues(current => { const next = { ...current }; delete next[parameter]; return next; }); setDirty(true);
  }
  async function saveDefinition() {
    if (!definition) return; setBusy(true);
    try { apply(await api.saveScenarioAcceptance(revisionId, { jobs: jobs.map(job => ({ ...job, requiredCredentials: job.requiredCredentials.map(name => name.trim()).filter(Boolean) })), parameters, values, bindings, expectedRevisionDigest: definition.revisionDigest })); onSaved(); notify('success', '业务验收已保存', '作业与参数变化已使旧测试证据失效。'); }
    catch (reason) { notify('error', '保存业务验收失败', displayError(reason)); } finally { setBusy(false); }
  }
  async function openFile(path: string) {
    if (fileDirty && !window.confirm('当前文件有未保存内容，确认放弃并打开其他文件？')) return;
    setBusy(true);
    try { const file = await api.scenarioAcceptanceFile(revisionId, path); setSelectedFile(file); setContent(file.content ?? ''); setFileDirty(false); }
    catch (reason) { notify('error', '读取验收文件失败', displayError(reason)); } finally { setBusy(false); }
  }
  async function saveFile(path: string, text: string, file?: File) {
    if (!definition || dirty) return; setBusy(true);
    try {
      const input = { path, ...(selectedJob?.legacyYamlSettings && selectedFile?.path === path && confirmYamlMigration ? { confirmYamlMigration: true } : {}), expectedSha256: definition.workspace.files.find(item => item.path === path)?.sha256 ?? '', expectedTreeSha256: definition.workspace.treeSha256, expectedRevisionDigest: definition.revisionDigest };
      // An opened editor always submits the SHA it read, even if a refreshed tree
      // now contains a newer file. The server's CAS rejects stale text safely.
      if (selectedFile?.path === path) input.expectedSha256 = selectedFile.sha256;
      const saved = file ? await api.uploadScenarioAcceptanceFile(revisionId, file, input) : await api.saveScenarioAcceptanceFile(revisionId, { ...input, content: text });
      apply(await api.scenarioAcceptance(revisionId)); setConfirmYamlMigration(false); setSelectedFile(saved); setContent(saved.content ?? text); setFileDirty(false); onSaved(); notify('success', '验收文件已保存', path);
    } catch (reason) { notify('error', '保存验收文件失败', displayError(reason)); } finally { setBusy(false); }
  }
  async function deleteFile() {
    if (!definition || !selectedFile || dirty || !window.confirm(`删除验收工作区文件 ${selectedFile.path}？`)) return; setBusy(true);
    try {
      await api.deleteScenarioAcceptanceFile(revisionId, { path: selectedFile.path, expectedSha256: selectedFile.sha256, expectedTreeSha256: definition.workspace.treeSha256, expectedRevisionDigest: definition.revisionDigest });
      apply(await api.scenarioAcceptance(revisionId)); setSelectedFile(undefined); setContent(''); setFileDirty(false); onSaved();
    } catch (reason) { notify('error', '删除验收文件失败', displayError(reason)); } finally { setBusy(false); }
  }
  if (error) return <ErrorBlock message={error} onRetry={() => { setError(undefined); void api.scenarioAcceptance(revisionId).then(apply).catch(reason => setError(displayError(reason))); }} />;
  if (!definition) return <LoadingBlock label="正在读取业务验收…" />;
  return <section className="scenario-acceptance panel">
    <header className="scenario-section-header"><div className="scenario-section-title"><span className="panel__icon panel__icon--cyan"><ClipboardCheck size={18} aria-hidden="true" /></span><div><h3>场景业务验收</h3><p>组件全部验证通过后，按顺序执行下列 Ansible 验收作业；任一失败立即停止。</p></div></div>{canEdit && <button className="button button--primary" disabled={busy || !dirty} onClick={() => void saveDefinition()}><Save size={15} /> 保存业务验收</button>}</header>
    {!jobs.length && <EmptyState title="尚未配置业务验收" description="至少添加一项业务验收，保存后录入其 Ansible 任务，才可完成测试与发布。" />}
    <div className="scenario-acceptance-jobs">{jobs.map((job, index) => <article className="scenario-acceptance-job" key={job.id}>
      <header><strong className="scenario-job-title"><span>{index + 1}</span>{job.name || '未命名验收'}</strong><div className="scenario-inline-actions">{canEdit && <><button aria-label={`上移 ${job.name}`} className="button button--quiet" disabled={busy || index === 0} onClick={() => { setJobs(moveAcceptanceJob(jobs, index, -1)); setDirty(true); }}><ArrowUp size={15} /></button><button aria-label={`下移 ${job.name}`} className="button button--quiet" disabled={busy || index === jobs.length - 1} onClick={() => { setJobs(moveAcceptanceJob(jobs, index, 1)); setDirty(true); }}><ArrowDown size={15} /></button><button aria-label={`删除 ${job.name}`} className="button button--danger-soft" disabled={busy} onClick={() => { setJobs(jobs.filter((_, i) => i !== index)); setDirty(true); }}><Trash2 size={15} /></button></>}</div></header>
      <fieldset disabled={!canEdit || busy} className="form-grid">
        <label><span>验收名称</span><input aria-label={`验收名称 ${index + 1}`} value={job.name} onChange={event => updateJob(index, { name: event.target.value })} /></label>
        <label><span>目标主机组</span><select aria-label={`验收主机组 ${index + 1}`} value={job.hostGroup} onChange={event => updateJob(index, { hostGroup: event.target.value })}><option value="">请选择</option>{hostGroups.map(group => <option key={group.id} value={group.value}>{group.label}</option>)}{job.hostGroup && !hostGroups.some(group => group.value === job.hostGroup) && <option value={job.hostGroup}>{job.hostGroup}（需复核）</option>}</select></label>
        <label className="span-2"><span>验收目的与成功条件</span><textarea aria-label={`验收目的 ${index + 1}`} rows={2} value={job.purpose} onChange={event => updateJob(index, { purpose: event.target.value })} /></label>
        <label><span>超时（秒）</span><input type="number" min={1} max={86400} value={job.timeoutSeconds} onChange={event => updateJob(index, { timeoutSeconds: Number(event.target.value) })} /></label>
        <label><span>风险等级</span><select value={job.riskLevel} onChange={event => updateJob(index, { riskLevel: event.target.value as ScenarioAcceptanceJob['riskLevel'] })}><option value="low">低风险</option><option value="medium">中风险</option><option value="high">高风险</option><option value="destructive">破坏性操作</option></select></label>
        <label><span>所需凭据引用名称（每行一项）</span><textarea rows={2} value={job.requiredCredentials.join('\n')} onChange={event => updateJob(index, { requiredCredentials: event.target.value.split('\n') })} /><small>只填写 CredentialRef 名称，由环境提供引用。{credentialNames.length > 0 ? `已有：${credentialNames.join('、')}` : ''}</small></label>
        <LegacyYamlNotice value={job.legacyYamlSettings}/><div className="scenario-checkboxes"><label><input type="checkbox" checked={job.become} onChange={event => updateJob(index, { become: event.target.checked })} /> 提权执行</label><label><input type="checkbox" checked={job.mayMutate} onChange={event => updateJob(index, { mayMutate: event.target.checked })} /> 允许临时业务操作</label></div>
      </fieldset>
      {job.mayMutate && <p className="inline-warning">在成功路径中清理临时资源；失败后保留现场，清理失败也会使验收失败。</p>}
      <div className="scenario-inline-actions"><StatusPill status={job.playbookSha256 ? 'ready' : 'pending'}>{job.playbookSha256 ? '已录入 Ansible 任务' : '待录入 Ansible 任务'}</StatusPill><button className="button button--secondary" disabled={busy || dirty || !job.playbook} onClick={() => { const path = `tasks/acceptance/${job.id}.yml`; if (definition.workspace.files.some(file => file.path === path)) void openFile(path); else { setSelectedFile({ releaseId: revisionId, path, sha256: '', sizeBytes: 0, mediaType: 'text/yaml', editable: true, content: '' }); setContent('- name: 验证业务响应\n  ansible.builtin.uri:\n    url: "{{ acceptance_url }}"\n    status_code: 200\n'); setFileDirty(true); } }}>编辑 Ansible 任务</button></div>
    </article>)}</div>
    {canEdit && <button className="button button--secondary" disabled={busy} onClick={() => { setJobs([...jobs, newAcceptanceJob(jobs.length, hostGroups[0]?.value)]); setDirty(true); }}><Plus size={15} /> 添加验收作业</button>}
    <section className="scenario-acceptance-parameters"><header className="scenario-section-title"><span className="panel__icon"><Settings2 size={18} aria-hidden="true" /></span><div><h3>验收参数</h3><p>录入类型化值，或绑定组件公开参数及环境字段。</p></div></header>{!parameters.length && <EmptyState title="尚未配置验收参数" description="需要传入业务地址等信息时，可添加参数并选择值的来源。" />}{parameters.map((parameter, index) => {
      const binding = bindings.find(item => item.parameter === parameter.name);
      const sourceOptions = nodes.flatMap(node => (releaseMap.get(node.data.releaseId)?.parameters ?? []).filter(item => item.visibility === 'public' && item.type === parameter.type).map(item => ({ value: `${node.id}::${item.name}`, label: `${node.data.label} · ${item.name}` })));
      return <article key={index} className="scenario-acceptance-parameter"><fieldset disabled={!canEdit || busy} className="form-grid">
        <label><span>参数名称</span><input aria-label={`验收参数名称 ${index + 1}`} value={parameter.name} onChange={event => updateParameter(index, { name: event.target.value })} /></label><label><span>说明</span><input value={parameter.description} onChange={event => updateParameter(index, { description: event.target.value })} /></label>
        <label><span>类型</span><select value={parameter.type} onChange={event => { updateParameter(index, { type: event.target.value as ParameterDefinition['type'], enum: undefined }); setBinding(parameter.name); }}><option value="string">文本</option><option value="boolean">开关</option><option value="integer">整数</option><option value="number">数值</option>{['array', 'object'].includes(parameter.type) && <option value={parameter.type}>{parameter.type}（受控选项）</option>}</select></label>
        <label><span>值的来源</span><select value={binding?.source ?? 'value'} onChange={event => setBinding(parameter.name, event.target.value === 'value' ? undefined : { parameter: parameter.name, source: event.target.value as 'node' | 'environment', sourceParameter: '' })}><option value="value">场景 Owner 填写</option><option value="node">节点公开参数</option><option value="environment">环境字段</option></select></label>
        <label className="span-2"><span>{binding ? '绑定来源' : '参数值'}</span>{binding?.source === 'node' ? <select value={binding.nodeId && binding.sourceParameter ? `${binding.nodeId}::${binding.sourceParameter}` : ''} onChange={event => { const [nodeId, sourceParameter] = event.target.value.split('::'); setBinding(parameter.name, { ...binding, nodeId, sourceParameter }); }}><option value="">请选择同类型公开参数</option>{sourceOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}{binding.sourceParameter && !sourceOptions.some(option => option.value === `${binding.nodeId}::${binding.sourceParameter}`) && <option value={`${binding.nodeId}::${binding.sourceParameter}`}>失效来源 · {binding.sourceParameter}</option>}</select> : binding?.source === 'environment' ? <select value={binding.sourceParameter} onChange={event => setBinding(parameter.name, { ...binding, sourceParameter: event.target.value })}><option value="">请选择环境字段</option>{environmentNames.map(name => <option key={name} value={name}>{name}</option>)}{binding.sourceParameter && !environmentNames.includes(binding.sourceParameter) && <option value={binding.sourceParameter}>{binding.sourceParameter}（当前环境未提供）</option>}</select> : <ParameterValueEditor parameter={parameter} value={values[parameter.name]} disabled={!canEdit} optional={!parameter.required} onChange={value => { setValues(current => { const next = { ...current }; if (value === undefined) delete next[parameter.name]; else next[parameter.name] = value; return next; }); setDirty(true); }} />}</label>
        <label className="scenario-checkboxes"><input type="checkbox" checked={parameter.required === true} onChange={event => updateParameter(index, { required: event.target.checked })} /> 必填</label>
      </fieldset>{canEdit && <button className="button button--danger-soft" onClick={() => { setParameters(parameters.filter((_, i) => i !== index)); setBinding(parameter.name); setDirty(true); }}><Trash2 size={15} /> 删除参数</button>}</article>;
    })}{canEdit && <button className="button button--secondary" disabled={busy} onClick={() => { setParameters([...parameters, { name: '', description: '', type: 'string', required: false, visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner' }]); setDirty(true); }}><Plus size={15} /> 添加验收参数</button>}</section>
    <section className="scenario-acceptance-workspace"><header className="scenario-section-title"><span className="panel__icon panel__icon--amber"><FileCode2 size={18} aria-hidden="true" /></span><div><h3>Ansible 任务与辅助文件</h3><p>入口路径由平台生成，工作区独立。请在验收 YAML 中自行采集 facts 并检查必要工具。</p></div></header>{dirty && <p className="inline-warning">请先保存作业和参数，再编辑或上传文件。</p>}
      <div className="scenario-workspace-layout"><nav aria-label="验收工作区文件">{definition.workspace.files.length ? definition.workspace.files.map(file => <button className={selectedFile?.path === file.path ? 'active' : ''} key={file.path} disabled={busy || dirty} onClick={() => void openFile(file.path)}>{file.path}</button>) : <p>工作区尚无文件。</p>}</nav><div>
        {selectedFile ? <><LegacyYamlNotice value={selectedJob?.legacyYamlSettings} confirmed={confirmYamlMigration} onConfirm={canEdit ? setConfirmYamlMigration : undefined}/><strong className="scenario-file-path">{selectedFile.path}</strong>{selectedFile.editable !== false ? <textarea aria-label="验收文件在线编辑器" className="code-editor" rows={17} value={content} disabled={!canEdit || busy || dirty} spellCheck={false} onChange={event => { setContent(event.target.value); setFileDirty(true); }} /> : <p>该文件不是可编辑文本，可以上传替换文件。</p>}{canEdit && <div className="scenario-inline-actions"><button className="button button--primary" disabled={busy || dirty || (!fileDirty && !confirmYamlMigration) || selectedFile.editable === false} onClick={() => void saveFile(selectedFile.path, content)}><Save size={15} /> 保存验收文件</button><label className="button button--secondary"><Upload size={15} /> 上传替换<input type="file" hidden disabled={busy || dirty} onChange={event => { const file = event.target.files?.[0]; if (file) void saveFile(selectedFile.path, '', file); event.target.value = ''; }} /></label><button className="button button--danger-soft" disabled={busy || dirty || !selectedFile.sha256} onClick={() => void deleteFile()}><Trash2 size={15} /> 删除文件</button></div>}</> : <EmptyState title="选择一个验收文件" description="从作业打开入口任务，或创建、上传辅助文件。" />}
      </div></div>{canEdit && <div className="scenario-workspace-create"><label><span>辅助文件相对路径</span><input aria-label="验收辅助文件路径" value={newPath} disabled={busy || dirty} onChange={event => setNewPath(event.target.value)} placeholder="templates/check.j2" /></label><button className="button button--secondary" disabled={busy || dirty || !newPath.trim()} onClick={() => { setSelectedFile({ releaseId: revisionId, path: newPath.trim(), sha256: '', sizeBytes: 0, mediaType: 'text/plain', editable: true }); setContent(''); setFileDirty(true); }}>新建文本文件</button><label className="button button--secondary"><Upload size={15} /> 上传辅助文件<input type="file" hidden disabled={busy || dirty || !newPath.trim()} onChange={event => { const file = event.target.files?.[0]; if (file) void saveFile(newPath.trim(), '', file); event.target.value = ''; }} /></label></div>}
    </section>
  </section>;
}
