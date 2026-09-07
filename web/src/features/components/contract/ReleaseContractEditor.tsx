import { GitBranch, Shield } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { api } from '../../../api/client';
import { DependencyContractList, DependencyEditor, parameterContractErrors, ParameterContractList, ParameterTable } from '../../../components/ParameterEditors';
import { displayError, useApp } from '../../../context/AppContext';
import type { Component, ComponentDependency, ComponentRelease, ParameterDefinition } from '../../../types/domain';
import { type ContractSection } from '../model';

export function ReleaseContractEditor({ release: initialRelease, components, focusSection = 'dependencies', onCancel, onSaved, onDirtyChange }: { release: ComponentRelease; components: Component[]; focusSection?: ContractSection; onCancel: () => void; onSaved: () => void; onDirtyChange: (dirty: boolean) => void }) {
  const [release] = useState(initialRelease);
  const { notify } = useApp();
  const [busy, setBusy] = useState(false);
  const [parameters, setParameters] = useState<ParameterDefinition[]>(release.parameters ?? []);
  const [dependencies, setDependencies] = useState<ComponentDependency[]>(release.dependencies ?? []);
  const [targetName, setTargetName] = useState('');
  const [targetDescription, setTargetDescription] = useState('');
  const [targetVisibility, setTargetVisibility] = useState<'internal' | 'public'>('internal');
  const [source, setSource] = useState('');
  const editorRef = useRef<HTMLDivElement>(null);
  const contractErrors = parameterContractErrors(parameters, dependencies, components.flatMap(item => item.releases ?? []));
  const dirty = JSON.stringify(parameters) !== JSON.stringify(release.parameters ?? []) || JSON.stringify(dependencies) !== JSON.stringify(release.dependencies ?? []);
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  const mappedTargets = new Set(dependencies.flatMap(dependency => dependency.parameterMappings ?? []).map(mapping => mapping.targetParameter));
  const orphaned = parameters.filter(parameter => parameter.valueProvider === 'upstream_mapping' && !mappedTargets.has(parameter.name));
  const sourceOptions = dependencies.flatMap(dependency => {
    const upstream = components.flatMap(component => component.releases ?? []).find(item => item.id === dependency.releaseId);
    return (upstream?.parameters ?? []).filter(parameter => parameter.visibility === 'public').map(parameter => ({ dependency, parameter, label: `${components.find(component => component.id === dependency.componentId)?.name} · ${upstream?.version} · ${parameter.name}` }));
  });
  useEffect(() => { (editorRef.current?.querySelector(`#contract-${focusSection}`) ?? editorRef.current)?.scrollIntoView?.({ behavior: 'smooth', block: 'start' }); }, [focusSection]);
  function cancel() { if (!dirty || window.confirm('当前区域有未保存修改，确认放弃？')) onCancel(); }
  function addReference() {
    const selected = sourceOptions.find(item => JSON.stringify([item.dependency.releaseId, item.parameter.name]) === source);
    if (!selected || !targetName.trim() || !targetDescription.trim()) return;
    if (parameters.some(parameter => parameter.name === targetName.trim())) { notify('error', '参数名已存在'); return; }
    const parameter: ParameterDefinition = { name: targetName.trim(), description: targetDescription.trim(), type: selected.parameter.type, visibility: targetVisibility, modifiable: false, valueProvider: 'upstream_mapping', required: selected.parameter.required };
    setParameters(items => [...items, parameter]);
    setDependencies(items => items.map(item => item.releaseId === selected.dependency.releaseId ? { ...item, parameterMappings: [...(item.parameterMappings ?? []), { upstreamParameter: selected.parameter.name, targetParameter: parameter.name }] } : item));
    setTargetName(''); setTargetDescription(''); setSource('');
  }
  async function save() {
    if (contractErrors.length) { notify('error', '合同存在未处理引用', contractErrors.join('；')); return; }
    setBusy(true);
    try {
      const originals = new Set((release.parameters ?? []).map(parameter => parameter.name));
      await api.patchReleaseContract(release.id, {
        section: focusSection, expectedDefinitionGeneration: release.definitionGeneration ?? 0,
        ...(focusSection === 'parameters' ? { parameters } : { dependencies, newParameters: parameters.filter(parameter => !originals.has(parameter.name)), removeParameters: (release.parameters ?? []).filter(parameter => !parameters.some(item => item.name === parameter.name)).map(parameter => parameter.name) }),
      });
      notify('success', focusSection === 'parameters' ? '参数合同已保存' : '直接依赖已保存'); onSaved();
    } catch (reason) { notify('error', '保存失败', displayError(reason)); } finally { setBusy(false); }
  }
  return <div id="release-contract-editor" className="contract-panels contract-panels--editing" ref={editorRef}>
    <article className="panel" id="contract-dependencies"><header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{release.version} · {focusSection === 'dependencies' ? '编辑上游版本与公开参数映射' : '当前依赖，仅供参考'}</p></div></div></header>
      {focusSection === 'dependencies' ? <><div className="contract-editor"><DependencyEditor dependencies={dependencies} components={components} currentParameters={parameters} currentComponentId={release.componentId} onChange={setDependencies} /></div>
        <details className="contract-reference-create"><summary>新增引用参数</summary><p>将新的本组件参数与上游公开参数一起保存。</p><div className="form-grid"><label><span>上游公开参数</span><select value={source} onChange={event => setSource(event.target.value)}><option value="">请先添加依赖并选择公开参数</option>{sourceOptions.map(item => <option key={JSON.stringify([item.dependency.releaseId, item.parameter.name])} value={JSON.stringify([item.dependency.releaseId, item.parameter.name])}>{item.label}</option>)}</select></label><label><span>本组件参数名</span><input value={targetName} onChange={event => setTargetName(event.target.value)} /></label><label><span>参数说明</span><input value={targetDescription} onChange={event => setTargetDescription(event.target.value)} /></label><label><span>可见性</span><select value={targetVisibility} onChange={event => setTargetVisibility(event.target.value as 'internal' | 'public')}><option value="internal">内部</option><option value="public">公开</option></select></label></div><button type="button" className="button button--secondary" disabled={!source || !targetName.trim() || !targetDescription.trim()} onClick={addReference}>添加引用参数</button></details>
        {orphaned.length > 0 && <div className="inline-warning"><div><strong>以下引用参数已失去映射来源</strong>{orphaned.map(parameter => <p key={parameter.name}>{parameter.name} <button type="button" className="icon-text" onClick={() => { if (window.confirm(`确认一并移除引用参数 ${parameter.name}？保存时仍会校验其他引用。`)) setParameters(items => items.filter(item => item.name !== parameter.name)); }}>移除引用参数</button></p>)}</div></div>}</> : <DependencyContractList dependencies={release.dependencies ?? []} components={components} />}
    </article>
    <article className="panel" id="contract-parameters"><header className="panel__header"><div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>{release.version} · {focusSection === 'parameters' ? '编辑参数定义与可见性；上游引用请在直接依赖中配置' : '当前参数，仅供参考'}</p></div></div></header>{focusSection === 'parameters' ? <div className="contract-editor"><ParameterTable parameters={parameters} onChange={setParameters} /></div> : <ParameterContractList release={{ ...release, parameters }} components={components} />}</article>
    {contractErrors.length > 0 && <div className="form-validation">{contractErrors.map(error => <span key={error}>{error}</span>)}</div>}
    <div className="contract-editor-actions"><button type="button" className="button button--quiet" disabled={busy} onClick={cancel}>取消</button><button type="button" className="button button--primary" disabled={busy || !!contractErrors.length} onClick={() => void save()}>{busy ? '保存中…' : focusSection === 'parameters' ? '保存参数合同' : '保存直接依赖'}</button></div>
  </div>;
}
