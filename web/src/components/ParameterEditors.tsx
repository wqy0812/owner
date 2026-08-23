import { useMemo } from 'react';
import type { Component, ComponentDependency, ComponentRelease, ParameterDefinition, ParameterMapping, ParameterType, ParameterVisibility } from '../types/domain';

const PARAMETER_TYPES: ParameterType[] = ['string', 'boolean', 'integer', 'number', 'object', 'array'];

export function emptyParameter(): ParameterDefinition {
  return { name: '', description: '', type: 'string', required: false, visibility: 'internal', enum: undefined };
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
   }
  const mapped = new Set<string>();
  const lockedComponents = new Set<string>();
  for (const dependency of dependencies) {
    if (!dependency.componentId.trim() || !dependency.releaseId.trim()) {
      errors.push('每项依赖必须锁定一个已发布的上游版本');
      continue;
    }
    const upstream = upstreams.find((release) => release.id === dependency.releaseId);
    if (!upstream || upstream.state !== 'released' || upstream.componentId !== dependency.componentId) {
      errors.push(`依赖 ${dependency.componentId} 必须锁定该组件的已发布版本`);
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
      if (!source || source.visibility !== 'public') errors.push(`映射来源 ${mapping.upstreamParameter} 必须是上游公开参数`);
      if (target && source && target.type !== source.type) errors.push(`${mapping.targetParameter} 与上游类型不一致`);
    }
  }
  return [...new Set(errors)];
}

export function ParameterTable({ parameters, onChange, disabled }: { parameters: ParameterDefinition[]; onChange: (parameters: ParameterDefinition[]) => void; disabled?: boolean }) {
  function update(index: number, patch: Partial<ParameterDefinition>) {
    onChange(parameters.map((item, current) => current === index ? { ...item, ...patch } : item));
  }
  return <div className="parameter-table">
    {parameters.map((parameter, index) => <article key={`${parameter.name}-${index}`} className={`parameter-card parameter-card--${parameter.visibility}`}>
      <div className="parameter-card__grid">
        <label><span>参数名称</span><input aria-label="参数名称" placeholder="例如 kubeInstallRoot" value={parameter.name} disabled={disabled} onChange={(event) => update(index, { name: event.target.value.trim() })} /></label>
        <label className="span-2"><span>说明</span><input aria-label="参数说明" placeholder="这个参数给谁用" value={parameter.description} disabled={disabled} onChange={(event) => update(index, { description: event.target.value })} /></label>
        <label><span>类型</span><select aria-label="参数类型" value={parameter.type} disabled={disabled} onChange={(event) => update(index, { type: event.target.value as ParameterType, defaultValue: undefined, enum: undefined, minLength: undefined })}>
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
        <label className="checkbox-field checkbox-field--inline"><input type="checkbox" checked={Boolean(parameter.required)} disabled={disabled} onChange={(event) => update(index, { required: event.target.checked })} /><span>运行时必须有值</span></label>
        <label><span>默认值</span><DefaultValueEditor parameter={parameter} disabled={disabled} onChange={(defaultValue) => update(index, { defaultValue })} /></label>
        <label><span>枚举</span><input aria-label="枚举" placeholder="逗号分隔" value={(parameter.enum ?? []).map((item) => String(item)).join(', ')} disabled={disabled} onChange={(event) => update(index, { enum: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) })} /></label>
        {parameter.type === 'string' ? <label><span>最小长度</span><input aria-label="最小长度" type="number" min={0} placeholder="minLength" value={parameter.minLength ?? ''} disabled={disabled} onChange={(event) => update(index, { minLength: event.target.value === '' ? undefined : Number(event.target.value) })} /></label> : <span />}
      </div>
      {!disabled && <button type="button" className="button button--quiet" onClick={() => onChange(parameters.filter((_, current) => current !== index))}>删除参数</button>}
    </article>)}
    {!disabled && <button type="button" className="button button--secondary" onClick={() => onChange([...parameters, emptyParameter()])}>新增参数</button>}
  </div>;
}

function DefaultValueEditor({ parameter, disabled, onChange }: { parameter: ParameterDefinition; disabled?: boolean; onChange: (value: unknown) => void }) {
  if (parameter.type === 'boolean') {
    return <select aria-label="默认值" value={parameter.defaultValue === undefined ? '' : String(Boolean(parameter.defaultValue))} disabled={disabled} onChange={(event) => onChange(event.target.value === '' ? undefined : event.target.value === 'true')}>
      <option value="">无默认值</option>
      <option value="true">true</option>
      <option value="false">false</option>
    </select>;
  }
  if (parameter.type === 'object' || parameter.type === 'array') {
    return <textarea aria-label="默认值 JSON" className="code-editor code-editor--small" disabled={disabled} defaultValue={parameter.defaultValue === undefined ? '' : JSON.stringify(parameter.defaultValue)} onBlur={(event) => {
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
  return <input aria-label="默认值" placeholder="默认值" disabled={disabled} value={parameter.defaultValue === undefined || parameter.defaultValue === null ? '' : String(parameter.defaultValue)} onChange={(event) => {
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
    .map((component) => ({ component, releases: (component.releases ?? []).filter((release) => release.state === 'released') }))
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
          <label><span>已发布版本</span><select aria-label="已发布版本" disabled={disabled || !dependency.componentId} value={dependency.releaseId} onChange={(event) => {
            update(index, { releaseId: event.target.value, parameterMappings: [] });
          }}>
            <option value="">{dependency.componentId ? '选择已发布版本' : '先选择上游组件'}</option>
            {selectedComponent?.releases.map((release) => <option key={release.id} value={release.id}>{release.version}</option>)}
          </select></label>
          <label><span>依赖用途</span><input aria-label="依赖用途" placeholder="例如复用 kubelet 安装目录" disabled={disabled} value={dependency.purpose ?? ''} onChange={(event) => update(index, { purpose: event.target.value })} /></label>
        </div>
        <div className="mapping-block">
          <strong>参数映射</strong>
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
                {currentParameters.filter((item) => item.name && (item.name === mapping.targetParameter || !usedTargets.has(item.name))).map((item) => <option key={item.name} value={item.name}>{item.name} · {item.type} · {item.visibility === 'public' ? '公开' : '内部'}</option>)}
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
          <small>{dependency.purpose || '运行时依赖'}</small>
        </header>
        {mappings.length ? mappings.map((mapping) => <p key={`${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">{describeParameterMapping(dependency, mapping, components)}</p>) : <p className="mapping-empty">只锁定上游版本，未引用其公开参数</p>}
      </article>;
    })}
  </div>;
}

export function ParameterContractList({ release, components }: { release?: ComponentRelease; components: Component[] }) {
  const parameters = release?.parameters ?? [];
  const sources = importedParameterSources(release, components);
  const consumers = downstreamParameterConsumers(release?.componentId, components);
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
          <small>{item.type} · {item.description}{item.defaultValue !== undefined ? ` · 默认 ${String(item.defaultValue)}` : ''}</small>
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
          <small>{item.type} · {item.description}{item.defaultValue !== undefined ? ` · 默认 ${String(item.defaultValue)}` : ''}</small>
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
      if (source?.defaultValue !== undefined) values[mapping.targetParameter] = typeof source.defaultValue === 'string' ? source.defaultValue : JSON.stringify(source.defaultValue);
    }
  }
  return values;
}
