import { ChevronDown } from 'lucide-react';
import { useId, useMemo, useState } from 'react';
import type { Component, ComponentDependency, ComponentRelease, ParameterDefinition, ParameterMapping, ParameterType, ParameterValueProvider, ParameterVisibility } from '../types/domain';

const PARAMETER_TYPES: ParameterType[] = ['string', 'boolean', 'integer', 'number', 'object', 'array'];
const VALUE_PROVIDER_LABELS: Record<ParameterValueProvider, string> = {
  component_owner: '组件 Owner 固定',
  scenario_owner: '集群 Owner 填写',
  environment_owner: '环境 Owner 填写',
  upstream_mapping: '上游映射提供',
};

export function emptyParameter(): ParameterDefinition {
  return { name: '', description: '', type: 'string', required: false, visibility: 'internal', modifiable: false, valueProvider: 'component_owner', fixedValue: '', enum: undefined };
}

export function resolveUpstream(dependency: ComponentDependency, components: Component[] = []) {
  return components
    .flatMap((component) => (component.releases ?? []).map((release) => ({ component, release })))
    .find((item) => item.release.id === dependency.releaseId);
}

export function upstreamLabel(dependency: ComponentDependency, components: Component[] = []) {
  const resolved = resolveUpstream(dependency, components);
  const name = resolved?.component.name ?? dependency.componentName ?? dependency.componentId;
  const version = resolved?.release.version ?? dependency.version;
  return version ? `${name} ${version}` : name;
}

export function describeParameterMapping(dependency: ComponentDependency, mapping: ParameterMapping, components: Component[] = []) {
  const source = mapping.upstreamParameter || '（未选择上游参数）';
  const target = mapping.targetParameter || '（未选择本组件参数）';
  return `本组件参数 ${target} 来自 ${upstreamLabel(dependency, components)} 的公开参数 ${source}`;
}

export function importedParameterSources(release: ComponentRelease | undefined, components: Component[] = []) {
  const sources = new Map<string, { dependency: ComponentDependency; mapping: ParameterMapping; label: string }>();
  for (const dependency of release?.dependencies ?? []) {
    for (const mapping of dependency.parameterMappings ?? []) {
      if (!mapping.targetParameter) continue;
      sources.set(mapping.targetParameter, {
        dependency,
        mapping,
        label: describeParameterMapping(dependency, mapping, components),
      });
    }
  }
  return sources;
}

export function downstreamParameterConsumers(componentId: string | undefined, components: Component[] = []) {
  if (!componentId) return [];
  const seen = new Set<string>();
  const consumers: Array<{ componentName: string; version?: string; upstreamParameter: string; targetParameter: string; label: string }> = [];
  for (const component of components) {
    const releases = [...(component.releases ?? [])].sort((left, right) => Number(right.state === 'released') - Number(left.state === 'released'));
    for (const release of releases) {
      for (const dependency of release.dependencies ?? []) {
        if (dependency.componentId !== componentId) continue;
        for (const mapping of dependency.parameterMappings ?? []) {
          const key = `${component.id}:${mapping.upstreamParameter}:${mapping.targetParameter}`;
          if (seen.has(key)) continue;
          seen.add(key);
          consumers.push({
            componentName: component.name,
            version: release.version,
            upstreamParameter: mapping.upstreamParameter,
            targetParameter: mapping.targetParameter,
            label: `${component.name}${release.version ? ` ${release.version}` : ''} 的 ${mapping.targetParameter} 引用本组件公开参数 ${mapping.upstreamParameter}`,
          });
        }
      }
    }
  }
  return consumers;
}

export function parameterContractErrors(parameters: ParameterDefinition[], dependencies: ComponentDependency[], upstreams: ComponentRelease[]): string[] {
  const errors: string[] = [];
  const seen = new Set<string>();
   for (const parameter of parameters) {
     if (!parameter.name.trim()) errors.push(`参数 ${parameter.name} 名称不能为空`);
     if (!parameter.description.trim()) errors.push(`参数 ${parameter.name} 说明不能为空`);
     if (seen.has(parameter.name)) errors.push(`参数 ${parameter.name} 重复`);
     seen.add(parameter.name);
     if (/(password|secret|token|private[_-]?key|encryption[_-]?key|credential)/i.test(parameter.name) && !parameter.name.toLowerCase().endsWith('_version')) {
       errors.push(`敏感参数 ${parameter.name} 必须使用 CredentialRef`);
     }
     if (parameter.valueProvider === 'component_owner' && (parameter.modifiable || parameter.fixedValue === undefined)) errors.push(`参数 ${parameter.name} 必须由组件固定且填写固定值`);
     if ((parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner') && !parameter.modifiable) errors.push(`参数 ${parameter.name} 分配给外部 Owner 后必须允许修改`);
     if ((parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner') && (parameter.type === 'object' || parameter.type === 'array') && !parameter.enum?.length) errors.push(`参数 ${parameter.name} 是结构化字段，必须提供受控枚举，不能让外部 Owner 编辑原始 JSON`);
     if (parameter.valueProvider === 'environment_owner' && !parameter.environmentBinding) errors.push(`环境参数 ${parameter.name} 必须绑定本组件的环境字段`);
     if (parameter.environmentBinding && parameter.environmentBinding.kind !== 'private') errors.push(`参数 ${parameter.name} 必须使用组件环境字段`);
    if (parameter.valueProvider === 'upstream_mapping' && parameter.modifiable) errors.push(`上游映射参数 ${parameter.name} 不允许人工修改`);
   }
  const mapped = new Set<string>();
  const lockedComponents = new Set<string>();
  for (const dependency of dependencies) {
    if (!dependency.componentId.trim() || !dependency.releaseId.trim()) {
      errors.push('每项依赖必须锁定一个可用的上游版本');
      continue;
    }
    const upstream = upstreams.find((release) => release.id === dependency.releaseId);
    if (!upstream || (upstream.state !== 'released' && upstream.state !== 'draft') || upstream.componentId !== dependency.componentId) {
      errors.push(`依赖 ${dependency.componentId} 必须锁定该组件的可用版本`);
      continue;
    }
    if (lockedComponents.has(dependency.componentId)) errors.push(`组件 ${dependency.componentId} 只能添加一项直接依赖`);
    lockedComponents.add(dependency.componentId);
    for (const mapping of dependency.parameterMappings ?? []) {
      if (mapped.has(mapping.targetParameter)) errors.push(`目标参数 ${mapping.targetParameter} 被映射多次`);
      mapped.add(mapping.targetParameter);
      const target = parameters.find((item) => item.name === mapping.targetParameter);
      const source = upstream?.parameters?.find((item) => item.name === mapping.upstreamParameter);
      if (!target) errors.push(`映射目标 ${mapping.targetParameter} 不存在`);
      if (target && target.valueProvider !== 'upstream_mapping') errors.push(`映射目标 ${mapping.targetParameter} 必须声明由上游映射提供`);
      if (!source || source.visibility !== 'public') errors.push(`映射来源 ${mapping.upstreamParameter} 必须是上游公开参数`);
      if (target && source && target.type !== source.type) errors.push(`${mapping.targetParameter} 与上游类型不一致`);
    }
  }
  for (const parameter of parameters.filter((item) => item.valueProvider === 'upstream_mapping')) {
    if (!mapped.has(parameter.name)) errors.push(`上游映射参数 ${parameter.name} 必须存在唯一映射`);
  }
  return [...new Set(errors)];
}

export function ParameterTable({ parameters, onChange, disabled }: { parameters: ParameterDefinition[]; onChange: (parameters: ParameterDefinition[]) => void; disabled?: boolean }) {
  const [expandedIndex, setExpandedIndex] = useState<number>();
  const tableId = useId();
  function update(index: number, patch: Partial<ParameterDefinition>) {
    onChange(parameters.map((item, current) => current === index ? { ...item, ...patch } : item));
  }
  function changeProvider(index: number, valueProvider: ParameterValueProvider) {
    const parameter = parameters[index];
    const base: ParameterDefinition = {
      ...parameter, valueProvider,
      modifiable: valueProvider === 'scenario_owner' || valueProvider === 'environment_owner',
      fixedValue: undefined, suggestedValue: undefined, testValue: undefined, environmentBinding: undefined,
    };
    if (valueProvider === 'component_owner') base.fixedValue = parameter.fixedValue ?? (parameter.type === 'boolean' ? false : '');
    if (valueProvider === 'environment_owner') base.environmentBinding = { kind: 'private' };
    onChange(parameters.map((item, current) => current === index ? base : item));
  }
  function removeParameter(index: number) {
    onChange(parameters.filter((_, current) => current !== index));
    setExpandedIndex((current) => current === undefined || current < index ? current : current === index ? undefined : current - 1);
  }
  function addParameter() {
    setExpandedIndex(parameters.length);
    onChange([...parameters, emptyParameter()]);
  }
  return <div className="parameter-table">
    {parameters.map((parameter, index) => {
      const expanded = expandedIndex === index;
      const displayName = parameter.name || `未命名参数 ${index + 1}`;
      const editorId = `${tableId}-parameter-${index}`;
      return <article key={`${parameter.name}-${index}`} className={`parameter-card parameter-card--${parameter.visibility}${expanded ? ' is-expanded' : ''}`}>
      <button type="button" className="parameter-card__summary" aria-label={`${expanded ? '收起' : '编辑'}参数 ${displayName}`} aria-expanded={expanded} aria-controls={editorId} onClick={() => setExpandedIndex(expanded ? undefined : index)}>
        <span className="parameter-card__identity"><strong>{displayName}</strong><small>{parameter.description || '尚未填写说明'}</small></span>
        <span className="parameter-card__meta">
          <em>{parameter.type}</em>
          <em className={`parameter-card__visibility parameter-card__visibility--${parameter.visibility}`}>{parameter.visibility === 'public' ? '公开' : '内部'}</em>
          <em>{VALUE_PROVIDER_LABELS[parameter.valueProvider]}</em>
          {parameter.required ? <em>必填</em> : null}
        </span>
        <span className="parameter-card__toggle">{expanded ? '收起' : '编辑'}<ChevronDown size={15} aria-hidden="true" /></span>
      </button>
      {expanded ? <div className="parameter-card__editor" id={editorId}>
      <div className="parameter-card__grid">
        <label><span>参数名称</span><input aria-label="参数名称" placeholder="例如 kubeInstallRoot" value={parameter.name} disabled={disabled} onChange={(event) => update(index, { name: event.target.value.trim() })} /></label>
        <label className="span-2"><span>说明</span><input aria-label="参数说明" placeholder="这个参数给谁用" value={parameter.description} disabled={disabled} onChange={(event) => update(index, { description: event.target.value })} /></label>
        <label><span>类型</span><select aria-label="参数类型" value={parameter.type} disabled={disabled} onChange={(event) => update(index, { type: event.target.value as ParameterType, fixedValue: undefined, suggestedValue: undefined, testValue: undefined, enum: undefined, minLength: undefined })}>
          {PARAMETER_TYPES.map((type) => <option key={type} value={type}>{type}</option>)}
        </select></label>
        <fieldset className="visibility-fieldset">
          <legend>可见性</legend>
          <div className="visibility-toggle" role="radiogroup" aria-label="可见性">
            <label className={parameter.visibility === 'internal' ? 'active' : ''}>
              <input aria-label="内部" type="radio" name={`visibility-${index}`} value="internal" checked={parameter.visibility === 'internal'} disabled={disabled} onChange={() => update(index, { visibility: 'internal' })} />
              <strong>仅本 Release 使用</strong>
              <small>下游不可引用</small>
            </label>
            <label className={parameter.visibility === 'public' ? 'active' : ''}>
              <input aria-label="公开" type="radio" name={`visibility-${index}`} value="public" checked={parameter.visibility === 'public'} disabled={disabled} onChange={() => update(index, { visibility: 'public' })} />
              <strong>允许下游映射引用</strong>
              <small>下游可显式映射使用</small>
            </label>
          </div>
        </fieldset>
        <label><span>值的负责人</span><select aria-label="值的负责人" value={parameter.valueProvider} disabled={disabled} onChange={(event) => changeProvider(index, event.target.value as ParameterValueProvider)}><option value="component_owner">组件 Owner 固定</option><option value="scenario_owner">集群 Owner 填写</option><option value="environment_owner">环境 Owner 填写</option><option value="upstream_mapping">上游映射提供</option></select></label>
        <label className="checkbox-field checkbox-field--inline"><input type="checkbox" checked={Boolean(parameter.modifiable)} disabled={disabled || parameter.valueProvider === 'component_owner' || parameter.valueProvider === 'upstream_mapping'} onChange={(event) => update(index, { modifiable: event.target.checked })} /><span>允许外部修改</span></label>
        <label className="checkbox-field checkbox-field--inline"><input type="checkbox" checked={Boolean(parameter.required)} disabled={disabled} onChange={(event) => update(index, { required: event.target.checked })} /><span>正式运行必须有值</span></label>
        {parameter.valueProvider === 'component_owner' ? <label><span>Release 固定值</span><ParameterValueEditor parameter={parameter} value={parameter.fixedValue} disabled={disabled} onChange={(fixedValue) => update(index, { fixedValue })} /></label> : null}
        {parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner' ? <label><span>建议值（不自动生效）</span><ParameterValueEditor parameter={parameter} value={parameter.suggestedValue} disabled={disabled} optional onChange={(suggestedValue) => update(index, { suggestedValue })} /></label> : null}
        {parameter.valueProvider !== 'component_owner' ? <label><span>组件独立测试值</span><ParameterValueEditor parameter={parameter} value={parameter.testValue} disabled={disabled} optional onChange={(testValue) => update(index, { testValue })} /></label> : null}
        {parameter.valueProvider === 'environment_owner' ? <p className="section-hint">由本组件定义，环境 Owner 按组件版本填写；其他组件通过公开参数映射引用。</p> : null}
        <label><span>枚举</span><input aria-label="枚举" placeholder="逗号分隔" value={(parameter.enum ?? []).map((item) => String(item)).join(', ')} disabled={disabled} onChange={(event) => update(index, { enum: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) })} /></label>
        {parameter.type === 'string' ? <label><span>最小长度</span><input aria-label="最小长度" type="number" min={0} placeholder="minLength" value={parameter.minLength ?? ''} disabled={disabled} onChange={(event) => update(index, { minLength: event.target.value === '' ? undefined : Number(event.target.value) })} /></label> : <span />}
      </div>
      {!disabled && <button type="button" className="button button--quiet" onClick={() => removeParameter(index)}>删除参数</button>}
      </div> : null}
    </article>;
    })}
    {!disabled && <button type="button" className="button button--secondary" onClick={addParameter}>新增参数</button>}
  </div>;
}

export function ParameterValueEditor({ parameter, value, disabled, optional, onChange }: { parameter: Pick<ParameterDefinition, 'name' | 'type' | 'enum'>; value: unknown; disabled?: boolean; optional?: boolean; onChange: (value: unknown) => void }) {
  if (parameter.enum?.length) {
    return <select aria-label={`${parameter.name} 的值`} value={value === undefined ? '' : JSON.stringify(value)} disabled={disabled} onChange={(event) => onChange(event.target.value === '' ? undefined : JSON.parse(event.target.value))}><option value="">{optional ? '未填写' : '请选择'}</option>{parameter.enum.map((item) => <option key={JSON.stringify(item)} value={JSON.stringify(item)}>{String(item)}</option>)}</select>;
  }
  if (parameter.type === 'boolean') {
    return <select aria-label={`${parameter.name} 的值`} value={value === undefined ? '' : String(Boolean(value))} disabled={disabled} onChange={(event) => onChange(event.target.value === '' ? undefined : event.target.value === 'true')}>
      <option value="">{optional ? '未填写' : '请选择'}</option>
      <option value="true">true</option>
      <option value="false">false</option>
    </select>;
  }
  if (parameter.type === 'object' || parameter.type === 'array') {
    return <textarea key={value === undefined ? 'empty' : JSON.stringify(value)} aria-label={`${parameter.name} 的 JSON 值`} className="code-editor code-editor--small" disabled={disabled} defaultValue={value === undefined ? '' : JSON.stringify(value)} onBlur={(event) => {
      const text = event.target.value.trim();
      if (!text) { onChange(undefined); return; }
      try {
        const parsed = JSON.parse(text);
        if (typeof parsed !== 'object') throw new Error('must be object/array');
        onChange(parsed);
      } catch {
        onChange(undefined);
      }
    }} />;
  }
  return <input aria-label={`${parameter.name} 的值`} type={parameter.type === 'integer' || parameter.type === 'number' ? 'number' : 'text'} step={parameter.type === 'integer' ? 1 : parameter.type === 'number' ? 'any' : undefined} placeholder={optional ? '未填写' : '填写值'} disabled={disabled} value={value === undefined || value === null ? '' : String(value)} onChange={(event) => {
    const text = event.target.value;
    if (text === '') { onChange(undefined); return; }
    if (parameter.type === 'integer' || parameter.type === 'number') {
      const numeric = Number(text);
      onChange(Number.isNaN(numeric) ? text : numeric);
      return;
    }
    onChange(text);
  }} />;
}

export function DependencyEditor({
  dependencies, components, currentParameters, currentComponentId, onChange, disabled,
}: {
  dependencies: ComponentDependency[];
  components: Component[];
  currentParameters: ParameterDefinition[];
  currentComponentId?: string;
  onChange: (dependencies: ComponentDependency[]) => void;
  disabled?: boolean;
}) {
  const upstreams = useMemo(() => components
    .filter((component) => component.id !== currentComponentId)
    .map((component) => ({ component, releases: (component.releases ?? []).filter((release) => release.state === 'released' || release.state === 'draft') }))
    .filter((item) => item.releases.length > 0), [components, currentComponentId]);
  function update(index: number, patch: Partial<ComponentDependency>) {
    onChange(dependencies.map((item, current) => current === index ? { ...item, ...patch } : item));
  }
  return <div className="dependency-editor">
    {dependencies.map((dependency, index) => {
      const selectedComponent = upstreams.find((item) => item.component.id === dependency.componentId);
      const selectedRelease = selectedComponent?.releases.find((release) => release.id === dependency.releaseId);
      const publicParameters = selectedRelease?.parameters?.filter((item) => item.visibility === 'public') ?? [];
      const usedTargets = new Set((dependency.parameterMappings ?? []).map((item) => item.targetParameter));
      const usedComponents = new Set(dependencies.filter((_, current) => current !== index).map((item) => item.componentId));
      return <article key={`${dependency.componentId}-${dependency.releaseId}-${index}`} className="dependency-card">
        <div className="dependency-card__grid">
          <label><span>上游组件</span><select aria-label="上游组件" disabled={disabled} value={dependency.componentId} onChange={(event) => {
            update(index, { componentId: event.target.value, releaseId: '', parameterMappings: [] });
          }}>
            <option value="">选择上游组件</option>
            {upstreams.filter(({ component }) => component.id === dependency.componentId || !usedComponents.has(component.id)).map(({ component }) => <option key={component.id} value={component.id}>{component.name}</option>)}
          </select></label>
          <label><span>可用版本</span><select aria-label="已发布版本" disabled={disabled || !dependency.componentId} value={dependency.releaseId} onChange={(event) => {
            update(index, { releaseId: event.target.value, parameterMappings: [] });
          }}>
            <option value="">{dependency.componentId ? '选择可用版本' : '先选择上游组件'}</option>
            {selectedComponent?.releases.map((release) => <option key={release.id} value={release.id}>{release.version} · {release.state === 'draft' ? 'Draft' : 'Released'}</option>)}
          </select></label>
          <label><span>引用方式</span><select aria-label="引用方式" disabled={disabled} value={dependency.kind ?? ''} onChange={(event) => update(index, { kind: event.target.value === 'configuration' ? 'configuration' : undefined })}><option value="">执行依赖及参数引用</option><option value="configuration">仅引用配置，不约束执行顺序</option></select></label>
          <label><span>依赖用途</span><input aria-label="依赖用途" placeholder="例如复用 kubelet 安装目录" disabled={disabled} value={dependency.purpose ?? ''} onChange={(event) => update(index, { purpose: event.target.value })} /></label>
        </div>
        <div className="mapping-block">
          <strong>{dependency.kind === 'configuration' ? '配置参数引用' : '参数映射'}</strong>
          <p>选择上游的公开参数，写入本组件的目标参数。内部参数不会出现在来源列表中。</p>
          {(dependency.parameterMappings ?? []).map((mapping, mappingIndex) => <div key={`${mapping.targetParameter}-${mappingIndex}`} className="mapping-row">
            <p className="parameter-lineage">{describeParameterMapping(dependency, mapping, components)}</p>
            <div className="mapping-row__fields">
              <label><span>上游公开参数</span><select aria-label="上游公开参数" disabled={disabled || !publicParameters.length} value={mapping.upstreamParameter} onChange={(event) => {
                const next = [...(dependency.parameterMappings ?? [])];
                next[mappingIndex] = { ...mapping, upstreamParameter: event.target.value };
                update(index, { parameterMappings: next });
              }}>
                <option value="">{publicParameters.length ? '选择上游公开参数' : '该上游没有公开参数'}</option>
                {publicParameters.map((item) => <option key={item.name} value={item.name}>{item.name} · {item.type} · {item.description}</option>)}
              </select></label>
              <label><span>本组件目标参数</span><select aria-label="本 Release 目标参数" disabled={disabled} value={mapping.targetParameter} onChange={(event) => {
                const next = [...(dependency.parameterMappings ?? [])];
                next[mappingIndex] = { ...mapping, targetParameter: event.target.value };
                update(index, { parameterMappings: next });
              }}>
                <option value="">选择本组件参数</option>
                {currentParameters.filter((item) => item.valueProvider === 'upstream_mapping' && item.name && (item.name === mapping.targetParameter || !usedTargets.has(item.name))).map((item) => <option key={item.name} value={item.name}>{item.name} · {item.type} · {item.visibility === 'public' ? '公开' : '内部'}</option>)}
              </select></label>
              {!disabled && <button type="button" className="button button--quiet" onClick={() => update(index, { parameterMappings: (dependency.parameterMappings ?? []).filter((_, current) => current !== mappingIndex) })}>删除映射</button>}
            </div>
          </div>)}
          {!publicParameters.length && selectedRelease ? <p className="mapping-empty">上游 {selectedComponent?.component.name} {selectedRelease.version} 没有公开参数，下游无法引用它的配置。</p> : null}
          {!disabled && <button type="button" className="button button--quiet" disabled={!selectedRelease} onClick={() => update(index, { parameterMappings: [...(dependency.parameterMappings ?? []), { upstreamParameter: '', targetParameter: '' }] })}>增加映射</button>}
        </div>
        {!disabled && <button type="button" className="button button--danger-soft" onClick={() => onChange(dependencies.filter((_, current) => current !== index))}>删除依赖</button>}
      </article>;
    })}
    {!disabled && <button type="button" className="button button--secondary" onClick={() => onChange([...dependencies, { componentId: '', releaseId: '', purpose: '', parameterMappings: [] }])}>新增依赖</button>}
  </div>;
}

export function mappingCount(release?: ComponentRelease) {
  return (release?.dependencies ?? []).reduce((count, dependency) => count + (dependency.parameterMappings ?? []).length, 0);
}

export function defaultContractRelease(releases: ComponentRelease[], latest?: ComponentRelease) {
  return releases.find((release) => mappingCount(release) > 0) ?? latest ?? releases[0];
}

export function ComponentMappingOverview({ releases, components }: { releases: ComponentRelease[]; components: Component[] }) {
  const items = releases.flatMap((release) => (release.dependencies ?? []).flatMap((dependency) => (dependency.parameterMappings ?? []).map((mapping) => ({ release, dependency, mapping }))));
  if (!items.length) return null;
  return <div className="mapping-overview">
    <strong>各版本参数来源</strong>
    {items.map(({ release, dependency, mapping }) => <p key={`${release.id}-${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">
      {release.version}：{describeParameterMapping(dependency, mapping, components)}
    </p>)}
  </div>;
}

export function DependencyContractList({ dependencies, components }: { dependencies: ComponentDependency[]; components: Component[] }) {
  if (!dependencies.length) return <div className="empty-state"><strong>没有直接依赖</strong></div>;
  return <div className="dependency-contract">
    {dependencies.map((dependency) => {
      const mappings = dependency.parameterMappings ?? [];
      return <article key={`${dependency.id ?? ''}-${dependency.componentId}-${dependency.releaseId}`} className="dependency-contract__item">
        <header>
          <strong>{upstreamLabel(dependency, components)}</strong>
          <small>{dependency.kind === 'configuration' ? '配置引用，不约束执行顺序' : dependency.purpose || '执行依赖'}</small>
        </header>
        {mappings.length ? mappings.map((mapping) => <p key={`${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">{describeParameterMapping(dependency, mapping, components)}</p>) : <p className="mapping-empty">只锁定上游版本，未引用其公开参数</p>}
      </article>;
    })}
  </div>;
}

export function ParameterContractList({ release, components, consumers: providedConsumers }: { release?: ComponentRelease; components: Component[]; consumers?: ReturnType<typeof downstreamParameterConsumers> }) {
  const parameters = release?.parameters ?? [];
  const sources = importedParameterSources(release, components);
  const consumers = providedConsumers ?? downstreamParameterConsumers(release?.componentId, components);
  const publicItems = parameters.filter((item) => item.visibility === 'public');
  const internalItems = parameters.filter((item) => item.visibility !== 'public');
  return <div className="parameter-preview">
    <section>
      <strong>公开参数</strong>
      <p className="section-hint">下游组件只能引用这里列出的参数</p>
      {publicItems.length ? publicItems.map((item) => {
        const usedBy = consumers.filter((consumer) => consumer.upstreamParameter === item.name);
        return <div key={item.name} className="parameter-preview__item">
          <div><span>{item.name}</span><em className="visibility-badge visibility-badge--public">公开</em></div>
          <small>{item.type} · {item.description} · {item.valueProvider}{item.fixedValue !== undefined ? ` · 固定 ${String(item.fixedValue)}` : ''}</small>
          {usedBy.length ? usedBy.map((consumer) => <small key={consumer.label} className="parameter-lineage">{consumer.label}</small>) : <small className="mapping-empty">尚未被下游引用</small>}
        </div>;
      }) : <div className="empty-state"><strong>没有公开参数</strong></div>}
    </section>
    <section>
      <strong>内部参数</strong>
      <p className="section-hint">只给本组件使用；如果来自上游，会标明具体来源</p>
      {internalItems.length ? internalItems.map((item) => {
        const source = sources.get(item.name);
        return <div key={item.name} className="parameter-preview__item">
          <div><span>{item.name}</span><em className="visibility-badge visibility-badge--internal">内部</em></div>
          <small>{item.type} · {item.description} · {item.valueProvider}{item.fixedValue !== undefined ? ` · 固定 ${String(item.fixedValue)}` : ''}</small>
          {source ? <small className="parameter-lineage">{source.label}</small> : null}
        </div>;
      }) : <div className="empty-state"><strong>没有内部参数</strong></div>}
    </section>
  </div>;
}

export function mappedParameterNames(release?: ComponentRelease): Set<string> {
  return new Set((release?.dependencies ?? []).flatMap((dependency) => (dependency.parameterMappings ?? []).map((item) => item.targetParameter)));
}

export function publicParameters(release?: ComponentRelease): ParameterDefinition[] {
  return (release?.parameters ?? []).filter((item) => item.visibility === 'public');
}

export function defaultFixtureValues(release?: ComponentRelease, components?: Component[]): Record<string, string> {
  const values: Record<string, string> = {};
  for (const dependency of release?.dependencies ?? []) {
    const upstream = components?.flatMap((component) => component.releases ?? []).find((item) => item.id === dependency.releaseId);
    for (const mapping of dependency.parameterMappings ?? []) {
      const source = upstream?.parameters?.find((item) => item.name === mapping.upstreamParameter);
      const testValue = source?.testValue ?? source?.fixedValue;
      if (testValue !== undefined) values[mapping.targetParameter] = typeof testValue === 'string' ? testValue : JSON.stringify(testValue);
    }
  }
  return values;
}
