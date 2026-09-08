import { Field } from './Field';
import { Input, Select, Radio, Checkbox, Button } from 'antd';
import { ChevronDown } from 'lucide-react';
import { useEffect, useId, useMemo, useState } from 'react';
import type { Component, ComponentDependency, ComponentRelease, ParameterDefinition, ParameterMapping, ParameterType, ParameterValueProvider } from '../types/domain';
const PARAMETER_TYPES: ParameterType[] = ['string', 'boolean', 'integer', 'number', 'object', 'array'];
const VALUE_PROVIDER_LABELS: Record<ParameterValueProvider, string> = {
    component_owner: '组件 Owner 固定',
    scenario_owner: '集群 Owner 填写',
    environment_owner: '环境 Owner 填写',
    upstream_mapping: '上游映射提供',
};
function matchesParameterType(value: unknown, type: ParameterType) {
    if (type === 'array') return Array.isArray(value);
    if (type === 'object') return value !== null && typeof value === 'object' && !Array.isArray(value);
    if (type === 'integer') return typeof value === 'number' && Number.isSafeInteger(value);
    if (type === 'number') return typeof value === 'number' && Number.isFinite(value);
    return typeof value === type;
}
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
    const sources = new Map<string, {
        dependency: ComponentDependency;
        mapping: ParameterMapping;
        label: string;
    }>();
    for (const dependency of release?.dependencies ?? []) {
        for (const mapping of dependency.parameterMappings ?? []) {
            if (!mapping.targetParameter)
                continue;
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
    if (!componentId)
        return [];
    const seen = new Set<string>();
    const consumers: Array<{
        componentName: string;
        version?: string;
        upstreamParameter: string;
        targetParameter: string;
        label: string;
    }> = [];
    for (const component of components) {
        const releases = [...(component.releases ?? [])].sort((left, right) => Number(right.state === 'released') - Number(left.state === 'released'));
        for (const release of releases) {
            for (const dependency of release.dependencies ?? []) {
                if (dependency.componentId !== componentId)
                    continue;
                for (const mapping of dependency.parameterMappings ?? []) {
                    const key = `${component.id}:${mapping.upstreamParameter}:${mapping.targetParameter}`;
                    if (seen.has(key))
                        continue;
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
        if (!parameter.name.trim())
            errors.push(`参数 ${parameter.name} 名称不能为空`);
        if (!parameter.description.trim())
            errors.push(`参数 ${parameter.name} 说明不能为空`);
        if (seen.has(parameter.name))
            errors.push(`参数 ${parameter.name} 重复`);
        seen.add(parameter.name);
        if (parameter.enum?.some(value => !matchesParameterType(value, parameter.type)))
            errors.push(`参数 ${parameter.name} 的枚举必须全部符合 ${parameter.type} 类型`);
        if (/(password|secret|token|private[_-]?key|encryption[_-]?key|credential)/i.test(parameter.name) && !parameter.name.toLowerCase().endsWith('_version')) {
            errors.push(`敏感参数 ${parameter.name} 必须使用 CredentialRef`);
        }
        if (parameter.valueProvider === 'component_owner' && (parameter.modifiable || parameter.fixedValue === undefined))
            errors.push(`参数 ${parameter.name} 必须由组件固定且填写固定值`);
        if ((parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner') && !parameter.modifiable)
            errors.push(`参数 ${parameter.name} 分配给外部 Owner 后必须允许修改`);
        if ((parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner') && (parameter.type === 'object' || parameter.type === 'array') && !parameter.enum?.length)
            errors.push(`参数 ${parameter.name} 是结构化字段，必须提供受控枚举，不能让外部 Owner 编辑原始 JSON`);
        if (parameter.valueProvider === 'environment_owner' && !parameter.environmentBinding)
            errors.push(`环境参数 ${parameter.name} 必须绑定本组件的环境字段`);
        if (parameter.environmentBinding && parameter.environmentBinding.kind !== 'private')
            errors.push(`参数 ${parameter.name} 必须使用组件环境字段`);
        if (parameter.valueProvider === 'upstream_mapping' && parameter.modifiable)
            errors.push(`上游映射参数 ${parameter.name} 不允许人工修改`);
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
        if (lockedComponents.has(dependency.componentId))
            errors.push(`组件 ${dependency.componentId} 只能添加一项直接依赖`);
        lockedComponents.add(dependency.componentId);
        for (const mapping of dependency.parameterMappings ?? []) {
            if (mapped.has(mapping.targetParameter))
                errors.push(`目标参数 ${mapping.targetParameter} 被映射多次`);
            mapped.add(mapping.targetParameter);
            const target = parameters.find((item) => item.name === mapping.targetParameter);
            const source = upstream?.parameters?.find((item) => item.name === mapping.upstreamParameter);
            if (!target)
                errors.push(`映射目标 ${mapping.targetParameter} 不存在`);
            if (target && target.valueProvider !== 'upstream_mapping')
                errors.push(`映射目标 ${mapping.targetParameter} 必须声明由上游映射提供`);
            if (!source || source.visibility !== 'public')
                errors.push(`映射来源 ${mapping.upstreamParameter} 必须是上游公开参数`);
            if (target && source && target.type !== source.type)
                errors.push(`${mapping.targetParameter} 与上游类型不一致`);
        }
    }
    for (const parameter of parameters.filter((item) => item.valueProvider === 'upstream_mapping')) {
        if (!mapped.has(parameter.name))
            errors.push(`上游映射参数 ${parameter.name} 必须存在唯一映射`);
    }
    return [...new Set(errors)];
}
export function ParameterTable({ parameters, onChange, disabled, onValidationChange }: {
    parameters: ParameterDefinition[];
    onChange: (parameters: ParameterDefinition[]) => void;
    disabled?: boolean;
    onValidationChange?: (valid: boolean) => void;
}) {
    const [expandedIndex, setExpandedIndex] = useState<number>();
    const [enumErrors, setEnumErrors] = useState<Record<number, string>>({});
    const [enumDrafts, setEnumDrafts] = useState<Record<number, string>>({});
    useEffect(() => onValidationChange?.(!Object.values(enumErrors).some(Boolean)), [enumErrors, onValidationChange]);
    const tableId = useId();
    function update(index: number, patch: Partial<ParameterDefinition>) {
        if (patch.type) {
            setEnumErrors(errors => { const next = { ...errors }; delete next[index]; return next; });
            setEnumDrafts(drafts => { const next = { ...drafts }; delete next[index]; return next; });
        }
        onChange(parameters.map((item, current) => current === index ? { ...item, ...patch } : item));
    }
    function changeProvider(index: number, valueProvider: ParameterValueProvider) {
        const parameter = parameters[index];
        const base: ParameterDefinition = {
            ...parameter, valueProvider,
            modifiable: valueProvider === 'scenario_owner' || valueProvider === 'environment_owner',
            fixedValue: undefined, suggestedValue: undefined, testValue: undefined, environmentBinding: undefined,
        };
        if (valueProvider === 'component_owner')
            base.fixedValue = parameter.fixedValue ?? (parameter.type === 'boolean' ? false : '');
        if (valueProvider === 'environment_owner')
            base.environmentBinding = { kind: 'private' };
        onChange(parameters.map((item, current) => current === index ? base : item));
    }
    function removeParameter(index: number) {
        setEnumDrafts(drafts => Object.fromEntries(Object.entries(drafts).filter(([key]) => Number(key) !== index).map(([key, text]) => [Number(key) > index ? Number(key) - 1 : Number(key), text])));
        setEnumErrors(errors => Object.fromEntries(Object.entries(errors).filter(([key]) => Number(key) !== index).map(([key, error]) => [Number(key) > index ? Number(key) - 1 : Number(key), error])));
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
            const enumRequired = (parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner') && (parameter.type === 'object' || parameter.type === 'array');
            return <article key={index} className={`parameter-card parameter-card--${parameter.visibility}${expanded ? ' is-expanded' : ''}`}>
      <button type="button" className="parameter-card__summary" aria-label={`${expanded ? '收起' : '编辑'}参数 ${displayName}`} aria-expanded={expanded} aria-controls={editorId} onClick={() => setExpandedIndex(expanded ? undefined : index)}>
        <span className="parameter-card__identity"><strong>{displayName}</strong><small>{parameter.description || '尚未填写说明'}</small></span>
        <span className="parameter-card__meta">
          <em>{parameter.type}</em>
          <em className={`parameter-card__visibility parameter-card__visibility--${parameter.visibility}`}>{parameter.visibility === 'public' ? '公开' : '内部'}</em>
          <em>{VALUE_PROVIDER_LABELS[parameter.valueProvider]}</em>
          {parameter.required ? <em>必填</em> : null}
        </span>
        <span className="parameter-card__toggle">{expanded ? '收起' : '编辑'}<ChevronDown size={15} aria-hidden="true"/></span>
      </button>
      {expanded ? <div className="parameter-card__editor" id={editorId}>
      <section className="parameter-card__section" aria-labelledby={`${editorId}-basics`}>
        <h3 id={`${editorId}-basics`}>基本信息</h3>
        <div className="parameter-card__fields parameter-card__fields--basics">
        <Field required label={"参数名称"}><Input aria-required aria-label="参数名称" placeholder="例如 kubeInstallRoot" value={parameter.name} disabled={disabled} onChange={(event) => update(index, { name: event.target.value.trim() })}/></Field>
        <Field required label={"类型"}><Select aria-required aria-label="参数类型" value={parameter.type} disabled={disabled} onChange={(selectedValue) => update(index, { type: selectedValue as ParameterType, fixedValue: undefined, suggestedValue: undefined, testValue: undefined, enum: undefined, minLength: undefined })} popupMatchSelectWidth={true}>
          {PARAMETER_TYPES.map((type) => <Select.Option key={type} value={type}>{type}</Select.Option>)}
        </Select></Field>
        <Field required label={"说明"}><Input aria-required aria-label="参数说明" placeholder="这个参数给谁用" value={parameter.description} disabled={disabled} onChange={(event) => update(index, { description: event.target.value })}/></Field>
        </div>
      </section>
      <section className="parameter-card__section" aria-labelledby={`${editorId}-rules`}>
        <h3 id={`${editorId}-rules`}>使用规则</h3>
        <div className="parameter-card__fields">
        <fieldset className="visibility-fieldset">
          <legend><span className="parameter-card__required" aria-hidden="true">*</span>可见性</legend>
          <div className="visibility-toggle" role="radiogroup" aria-required="true" aria-label="可见性">
            <Radio className={parameter.visibility === 'internal' ? 'active' : ''} aria-label="内部" name={`visibility-${index}`} value="internal" checked={parameter.visibility === 'internal'} disabled={disabled} onChange={() => update(index, { visibility: 'internal' })}><span className="visibility-toggle__copy"><strong>仅本 Release 使用</strong><small>下游不可引用</small></span></Radio>
            <Radio className={parameter.visibility === 'public' ? 'active' : ''} aria-label="公开" name={`visibility-${index}`} value="public" checked={parameter.visibility === 'public'} disabled={disabled} onChange={() => update(index, { visibility: 'public' })}><span className="visibility-toggle__copy"><strong>允许下游映射引用</strong><small>下游可显式映射使用</small></span></Radio>
          </div>
        </fieldset>
        <div className="parameter-card__provider">
        <Field required label={"值的负责人"}><Select aria-required aria-label="值的负责人" value={parameter.valueProvider} disabled={disabled} onChange={(selectedValue) => changeProvider(index, selectedValue as ParameterValueProvider)} popupMatchSelectWidth={true}><Select.Option value="component_owner">组件 Owner 固定</Select.Option><Select.Option value="scenario_owner">集群 Owner 填写</Select.Option><Select.Option value="environment_owner">环境 Owner 填写</Select.Option><Select.Option value="upstream_mapping">上游映射提供</Select.Option></Select></Field>
        <div className="parameter-card__flags">
          <Checkbox checked={Boolean(parameter.modifiable)} disabled={disabled || parameter.valueProvider === 'component_owner' || parameter.valueProvider === 'upstream_mapping'} onChange={(event) => update(index, { modifiable: event.target.checked })}>允许外部修改</Checkbox>
          <Checkbox checked={Boolean(parameter.required)} disabled={disabled} onChange={(event) => update(index, { required: event.target.checked })}>正式运行必须有值</Checkbox>
        </div>
        </div>
        </div>
      </section>
      <section className="parameter-card__section" aria-labelledby={`${editorId}-values`}>
        <h3 id={`${editorId}-values`}>参数值</h3>
        <div className="parameter-card__fields">
        {parameter.valueProvider === 'component_owner' ? <Field required label="Release 固定值"><ParameterValueEditor parameter={parameter} value={parameter.fixedValue} disabled={disabled} onChange={(fixedValue) => update(index, { fixedValue })}/></Field> : null}
        {parameter.valueProvider === 'scenario_owner' || parameter.valueProvider === 'environment_owner' ? <Field label="建议值（不自动生效）"><ParameterValueEditor parameter={parameter} value={parameter.suggestedValue} disabled={disabled} optional onChange={(suggestedValue) => update(index, { suggestedValue })}/></Field> : null}
        {parameter.valueProvider !== 'component_owner' ? <Field label="组件独立测试值"><ParameterValueEditor parameter={parameter} value={parameter.testValue} disabled={disabled} optional onChange={(testValue) => update(index, { testValue })}/></Field> : null}
        {parameter.valueProvider === 'environment_owner' ? <p className="section-hint parameter-card__hint">由本组件定义，环境 Owner 按组件版本填写；其他组件通过公开参数映射引用。</p> : null}
        </div>
      </section>
      <section className="parameter-card__section" aria-labelledby={`${editorId}-validation`}>
        <h3 id={`${editorId}-validation`}>校验约束</h3>
        <div className="parameter-card__fields">
        <Field required={enumRequired} label={"枚举"}><ParameterEnumEditor key={parameter.type} parameter={parameter} required={enumRequired} disabled={disabled} onChange={values => update(index, { enum: values })} text={enumDrafts[index]} onTextChange={text => setEnumDrafts(drafts => ({ ...drafts, [index]: text }))} error={enumErrors[index]} onError={error => setEnumErrors(errors => ({ ...errors, [index]: error }))}/></Field>
        {parameter.type === 'string' ? <Field label={"最小长度"}><Input aria-label="最小长度" type="number" min={0} placeholder="minLength" value={parameter.minLength ?? ''} disabled={disabled} onChange={(event) => update(index, { minLength: event.target.value === '' ? undefined : Number(event.target.value) })}/></Field> : null}
        </div>
      </section>
      {!disabled && <div className="parameter-card__actions"><Button danger onClick={() => removeParameter(index)} htmlType={"button"} type="text">删除参数</Button></div>}
      </div> : null}
    </article>;
        })}
    {!disabled && <Button className="button button--secondary" onClick={addParameter} htmlType={"button"} type="default">新增参数</Button>}
  </div>;
}
function ParameterEnumEditor({ parameter, required, disabled, onChange, error, onError, text, onTextChange }: {
    parameter: ParameterDefinition; required: boolean; disabled?: boolean;
    onChange: (values: unknown[]) => void; error?: string; onError: (error: string) => void; text?: string; onTextChange: (text: string) => void;
}) {
    const format = () => parameter.type === 'string' ? (parameter.enum ?? []).join(', ') : parameter.enum?.length ? JSON.stringify(parameter.enum) : '';
    return <><Input aria-required={required} aria-invalid={!!error} aria-label="枚举" disabled={disabled} value={text ?? format()}
        placeholder={parameter.type === 'string' ? '逗号分隔' : parameter.type === 'object' ? '[{"mode":"safe"},{"mode":"fast"}]' : parameter.type === 'array' ? '[["a"],["b","c"]]' : '[1,2]'}
        onChange={event => {
            const next = event.target.value; onTextChange(next);
            try {
                const values = parameter.type === 'string' ? next.split(',').map(value => value.trim()).filter(Boolean) : JSON.parse(next.trim() || '[]');
                if (!Array.isArray(values) || values.some(value => !matchesParameterType(value, parameter.type))) throw new Error();
                onError(''); onChange(values);
            } catch { onError(`请输入元素类型为 ${parameter.type} 的 JSON 数组`); }
        }}/>{parameter.type !== 'string' && <small>填写 JSON 数组，每项都是一个可选值。</small>}{error && <small role="alert">{error}</small>}</>;
}
export function ParameterValueEditor({ id, parameter, value, disabled, optional, onChange }: {
    id?: string;
    parameter: Pick<ParameterDefinition, 'name' | 'type' | 'enum'>;
    value: unknown;
    disabled?: boolean;
    optional?: boolean;
    onChange: (value: unknown) => void;
}) {
    if (parameter.enum?.length) {
        return <Select id={id} aria-label={`${parameter.name} 的值`} value={value === undefined ? '' : JSON.stringify(value)} disabled={disabled} onChange={(selectedValue) => onChange(selectedValue === '' ? undefined : JSON.parse(selectedValue))} popupMatchSelectWidth={true}><Select.Option value="">{optional ? '未填写' : '请选择'}</Select.Option>{parameter.enum.map((item) => <Select.Option key={JSON.stringify(item)} value={JSON.stringify(item)}>{typeof item === 'object' ? JSON.stringify(item) : String(item)}</Select.Option>)}</Select>;
    }
    if (parameter.type === 'boolean') {
        return <Select id={id} aria-label={`${parameter.name} 的值`} value={value === undefined ? '' : String(Boolean(value))} disabled={disabled} onChange={(selectedValue) => onChange(selectedValue === '' ? undefined : selectedValue === 'true')} popupMatchSelectWidth={true}>
      <Select.Option value="">{optional ? '未填写' : '请选择'}</Select.Option>
      <Select.Option value="true">true</Select.Option>
      <Select.Option value="false">false</Select.Option>
    </Select>;
    }
    if (parameter.type === 'object' || parameter.type === 'array') {
        return <Input.TextArea id={id} key={value === undefined ? 'empty' : JSON.stringify(value)} aria-label={`${parameter.name} 的 JSON 值`} className="code-editor code-editor--small" disabled={disabled} defaultValue={value === undefined ? '' : JSON.stringify(value)} onBlur={(event) => {
                const text = event.target.value.trim();
                if (!text) {
                    onChange(undefined);
                    return;
                }
                try {
                    const parsed = JSON.parse(text);
                    if (typeof parsed !== 'object')
                        throw new Error('must be object/array');
                    onChange(parsed);
                }
                catch {
                    onChange(undefined);
                }
            }}/>;
    }
    return <Input id={id} aria-label={`${parameter.name} 的值`} type={parameter.type === 'integer' || parameter.type === 'number' ? 'number' : 'text'} step={parameter.type === 'integer' ? 1 : parameter.type === 'number' ? 'any' : undefined} placeholder={optional ? '未填写' : '填写值'} disabled={disabled} value={value === undefined || value === null ? '' : String(value)} onChange={(event) => {
            const text = event.target.value;
            if (text === '') {
                onChange(undefined);
                return;
            }
            if (parameter.type === 'integer' || parameter.type === 'number') {
                const numeric = Number(text);
                onChange(Number.isNaN(numeric) ? text : numeric);
                return;
            }
            onChange(text);
        }}/>;
}
export function DependencyEditor({ dependencies, components, currentParameters, currentComponentId, onChange, disabled, }: {
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
          <Field label={"上游组件"}><Select aria-label="上游组件" disabled={disabled} value={dependency.componentId} onChange={(selectedValue) => {
                update(index, { componentId: selectedValue, releaseId: '', parameterMappings: [] });
            }} popupMatchSelectWidth={true}>
            <Select.Option value="">选择上游组件</Select.Option>
            {upstreams.filter(({ component }) => component.id === dependency.componentId || !usedComponents.has(component.id)).map(({ component }) => <Select.Option key={component.id} value={component.id}>{component.name}</Select.Option>)}
          </Select></Field>
          <Field label={"可用版本"}><Select aria-label="已发布版本" disabled={disabled || !dependency.componentId} value={dependency.releaseId} onChange={(selectedValue) => {
                update(index, { releaseId: selectedValue, parameterMappings: [] });
            }} popupMatchSelectWidth={true}>
            <Select.Option value="">{dependency.componentId ? '选择可用版本' : '先选择上游组件'}</Select.Option>
            {selectedComponent?.releases.map((release) => <Select.Option key={release.id} value={release.id}>{release.version} · {release.state === 'draft' ? 'Draft' : 'Released'}</Select.Option>)}
          </Select></Field>
          <Field label={"引用方式"}><Select aria-label="引用方式" disabled={disabled} value={dependency.kind} onChange={(selectedValue) => update(index, { kind: selectedValue === 'configuration' ? 'configuration' : 'execution' })} popupMatchSelectWidth={true}><Select.Option value="execution">执行依赖及参数引用</Select.Option><Select.Option value="configuration">仅引用配置，不约束执行顺序</Select.Option></Select></Field>
          <Field label={"依赖用途"}><Input aria-label="依赖用途" placeholder="例如复用 kubelet 安装目录" disabled={disabled} value={dependency.purpose ?? ''} onChange={(event) => update(index, { purpose: event.target.value })}/></Field>
        </div>
        <div className="mapping-block">
          <strong>{dependency.kind === 'configuration' ? '配置参数引用' : '参数映射'}</strong>
          <p>选择上游的公开参数，写入本组件的目标参数。内部参数不会出现在来源列表中。</p>
          {(dependency.parameterMappings ?? []).map((mapping, mappingIndex) => <div key={`${mapping.targetParameter}-${mappingIndex}`} className="mapping-row">
            <p className="parameter-lineage">{describeParameterMapping(dependency, mapping, components)}</p>
            <div className="mapping-row__fields">
              <Field label={"上游公开参数"}><Select aria-label="上游公开参数" disabled={disabled || !publicParameters.length} value={mapping.upstreamParameter} onChange={(selectedValue) => {
                    const next = [...(dependency.parameterMappings ?? [])];
                    next[mappingIndex] = { ...mapping, upstreamParameter: selectedValue };
                    update(index, { parameterMappings: next });
                }} popupMatchSelectWidth={true}>
                <Select.Option value="">{publicParameters.length ? '选择上游公开参数' : '该上游没有公开参数'}</Select.Option>
                {publicParameters.map((item) => <Select.Option key={item.name} value={item.name}>{item.name} · {item.type} · {item.description}</Select.Option>)}
              </Select></Field>
              <Field label={"本组件目标参数"}><Select aria-label="本 Release 目标参数" disabled={disabled} value={mapping.targetParameter} onChange={(selectedValue) => {
                    const next = [...(dependency.parameterMappings ?? [])];
                    next[mappingIndex] = { ...mapping, targetParameter: selectedValue };
                    update(index, { parameterMappings: next });
                }} popupMatchSelectWidth={true}>
                <Select.Option value="">选择本组件参数</Select.Option>
                {currentParameters.filter((item) => item.valueProvider === 'upstream_mapping' && item.name && (item.name === mapping.targetParameter || !usedTargets.has(item.name))).map((item) => <Select.Option key={item.name} value={item.name}>{item.name} · {item.type} · {item.visibility === 'public' ? '公开' : '内部'}</Select.Option>)}
              </Select></Field>
              {!disabled && <Button className="button button--quiet" onClick={() => update(index, { parameterMappings: (dependency.parameterMappings ?? []).filter((_, current) => current !== mappingIndex) })} htmlType={"button"} type="default">删除映射</Button>}
            </div>
          </div>)}
          {!publicParameters.length && selectedRelease ? <p className="mapping-empty">上游 {selectedComponent?.component.name} {selectedRelease.version} 没有公开参数，下游无法引用它的配置。</p> : null}
          {!disabled && <Button className="button button--quiet" disabled={!selectedRelease} onClick={() => update(index, { parameterMappings: [...(dependency.parameterMappings ?? []), { upstreamParameter: '', targetParameter: '' }] })} htmlType={"button"} type="default">增加映射</Button>}
        </div>
        {!disabled && <Button className="button button--danger-soft" onClick={() => onChange(dependencies.filter((_, current) => current !== index))} htmlType={"button"} type="default" danger>删除依赖</Button>}
      </article>;
        })}
    {!disabled && <Button className="button button--secondary" onClick={() => onChange([...dependencies, { kind: 'execution', componentId: '', releaseId: '', purpose: '', parameterMappings: [] }])} htmlType={"button"} type="default">新增依赖</Button>}
  </div>;
}
export function mappingCount(release?: ComponentRelease) {
    return (release?.dependencies ?? []).reduce((count, dependency) => count + (dependency.parameterMappings ?? []).length, 0);
}
export function defaultContractRelease(releases: ComponentRelease[], latest?: ComponentRelease) {
    return releases.find((release) => mappingCount(release) > 0) ?? latest ?? releases[0];
}
export function ComponentMappingOverview({ releases, components }: {
    releases: ComponentRelease[];
    components: Component[];
}) {
    const items = releases.flatMap((release) => (release.dependencies ?? []).flatMap((dependency) => (dependency.parameterMappings ?? []).map((mapping) => ({ release, dependency, mapping }))));
    if (!items.length)
        return null;
    return <div className="mapping-overview">
    <strong>各版本参数来源</strong>
    {items.map(({ release, dependency, mapping }) => <p key={`${release.id}-${mapping.upstreamParameter}-${mapping.targetParameter}`} className="parameter-lineage">
      {release.version}：{describeParameterMapping(dependency, mapping, components)}
    </p>)}
  </div>;
}
export function DependencyContractList({ dependencies, components }: {
    dependencies: ComponentDependency[];
    components: Component[];
}) {
    if (!dependencies.length)
        return <div className="empty-state"><strong>没有直接依赖</strong></div>;
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
export function ParameterContractList({ release, components, consumers: providedConsumers }: {
    release?: ComponentRelease;
    components: Component[];
    consumers?: ReturnType<typeof downstreamParameterConsumers>;
}) {
    const parameters = release?.parameters ?? [];
    const sources = importedParameterSources(release, components);
    const consumers = providedConsumers ?? downstreamParameterConsumers(release?.componentId, components);
    const publicItems = parameters.filter((item) => item.visibility === 'public');
    const internalItems = parameters.filter((item) => item.visibility !== 'public');
    const formatValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value, null, 2);
    return <div className="parameter-contract-list">
    {([{ title: '公开参数', items: publicItems, hint: '下游组件可通过映射引用' }, { title: '内部参数', items: internalItems, hint: '仅供本组件使用' }]).map(group => <section key={group.title}>
      <header className="parameter-contract-list__heading"><strong>{group.title}</strong><span>{group.items.length}</span><small>{group.hint}</small></header>
      {group.items.length ? group.items.map(item => {
                const usedBy = consumers.filter(consumer => consumer.upstreamParameter === item.name);
                const source = sources.get(item.name);
                return <details className="contract-parameter" key={`${release?.id}:${item.name}`}>
          <summary aria-label={`查看参数 ${item.name}`}>
            <span className="contract-parameter__identity"><strong>{item.name}</strong><small title={item.description}>{item.description || '暂无说明'}</small></span>
            <span className="contract-parameter__meta"><span>{VALUE_PROVIDER_LABELS[item.valueProvider]}</span>{item.visibility === 'public' && <span>{usedBy.length} 项引用</span>}</span>
            <ChevronDown size={15} aria-hidden="true"/>
          </summary>
          <div className="contract-parameter__details">
            <p>{item.description || '暂无说明'}</p>
            <dl><div><dt>类型</dt><dd>{item.type}</dd></div><div><dt>值的负责人</dt><dd>{VALUE_PROVIDER_LABELS[item.valueProvider]}</dd></div><div><dt>修改规则</dt><dd>{item.modifiable ? '允许负责人修改' : '不允许外部修改'} · {item.required ? '正式运行必填' : '可选'}</dd></div></dl>
            {([{ label: '固定值', value: item.fixedValue }, { label: '建议值（不自动生效）', value: item.suggestedValue }, { label: '独立测试值', value: item.testValue }]).filter(entry => entry.value !== undefined).map(entry => <div className="contract-parameter__value" key={entry.label}><strong>{entry.label}</strong><pre>{formatValue(entry.value)}</pre></div>)}
            {source && <div><strong>上游来源</strong><p className="parameter-lineage">{source.label}</p></div>}
            {item.visibility === 'public' && <div><strong>下游引用</strong>{usedBy.length ? <ul>{usedBy.map(consumer => <li key={consumer.label}><span>{consumer.componentName} {consumer.version}</span><code>{consumer.targetParameter}</code></li>)}</ul> : <p>尚未被下游引用</p>}</div>}
          </div>
        </details>;
            }) : <p className="contract-parameter__empty">没有{group.title}</p>}
    </section>)}
  </div>;
}
export function mappedParameterNames(release?: ComponentRelease): Set<string> {
    return new Set((release?.dependencies ?? []).flatMap((dependency) => (dependency.parameterMappings ?? []).map((item) => item.targetParameter)));
}
