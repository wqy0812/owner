import { COMPONENT_LAYERS } from '../types/componentClassification';
import type {
  ActionDefinition,
  Component,
  ComponentDependency,
  ComponentRelease,
  ParameterDefinition,
} from '../types/domain';

export type ComponentImportDependency = Omit<ComponentDependency, 'componentId' | 'releaseId'> & { componentSlug: string };

export type ComponentImportEntry = {
  component: Pick<Component, 'name' | 'layer' | 'tags'> & { slug: string; description?: string };
  release: Omit<Partial<ComponentRelease>, 'dependencies'> & { dependencies?: ComponentImportDependency[] };
  playbooks?: Array<{ filename: string; content: string }>;
};

const ACTION_TYPES = new Set<ActionDefinition['type']>(['inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall']);
const PARAMETER_TYPES = new Set<ParameterDefinition['type']>(['string', 'integer', 'number', 'boolean', 'object', 'array']);
const RISK_LEVELS = new Set<NonNullable<ComponentRelease['riskLevel']>>(['low', 'medium', 'high', 'destructive']);
const PLAYBOOK_FILENAME = /^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:yml|yaml)$/;
const SLUG = /^[a-z0-9][a-z0-9-]*$/;
const MAX_PLAYBOOK_BYTES = 1 << 20;
const SENSITIVE_KEY = /(password|passwd|secret|token|private[_-]?key|encryption[_-]?key|credential)/i;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function nonEmptyString(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${label}不能为空。`);
  return value.trim();
}

function assertOnlyKeys(value: Record<string, unknown>, allowed: readonly string[], label: string) {
  const accepted = new Set(allowed);
  const unknown = Object.keys(value).find((key) => !accepted.has(key));
  if (unknown) throw new Error(`${label}包含不支持字段 ${unknown}。`);
}

function optionalArray(value: unknown, label: string): unknown[] {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error(`${label}必须是数组。`);
  return value;
}

function optionalString(value: unknown, label: string): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== 'string') throw new Error(`${label}必须是字符串。`);
  return value.trim();
}

function optionalBoolean(value: unknown, label: string, fallback = false): boolean {
  if (value === undefined) return fallback;
  if (typeof value !== 'boolean') throw new Error(`${label}必须是布尔值。`);
  return value;
}

function stringArray(value: unknown, label: string): string[] {
  const items = optionalArray(value, label).map((item, index) => nonEmptyString(item, `${label}[${index}]`));
  if (new Set(items).size !== items.length) throw new Error(`${label}不能包含重复值。`);
  return items;
}

function matchesParameterType(value: unknown, type: ParameterDefinition['type']): boolean {
  switch (type) {
    case 'string': return typeof value === 'string';
    case 'boolean': return typeof value === 'boolean';
    case 'integer': return typeof value === 'number' && Number.isInteger(value);
    case 'number': return typeof value === 'number' && Number.isFinite(value);
    case 'object': return isRecord(value);
    case 'array': return Array.isArray(value);
  }
}

function parameterValuesEqual(left: unknown, right: unknown) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function isSensitiveKey(value: string) {
  return !value.toLowerCase().replaceAll('-', '_').endsWith('_version') && SENSITIVE_KEY.test(value);
}

function findSensitivePath(value: unknown, prefix = ''): string | undefined {
  if (Array.isArray(value)) {
    for (const [index, child] of value.entries()) {
      const found = findSensitivePath(child, `${prefix}[${index}]`);
      if (found) return found;
    }
  } else if (isRecord(value)) {
    for (const [key, child] of Object.entries(value)) {
      const path = prefix ? `${prefix}.${key}` : key;
      if (isSensitiveKey(key)) return path;
      const found = findSensitivePath(child, path);
      if (found) return found;
    }
  }
  return undefined;
}

function parseEntry(value: unknown, index: number): ComponentImportEntry {
  if (!isRecord(value) || !isRecord(value.component) || !isRecord(value.release)) {
    throw new Error(`第 ${index + 1} 项必须包含 component 和 release 对象。`);
  }
  const component = value.component;
  const release = value.release;
  assertOnlyKeys(value, ['component', 'release', 'playbooks'], `第 ${index + 1} 项`);
  assertOnlyKeys(component, ['name', 'slug', 'description', 'layer', 'tags'], `第 ${index + 1} 项 component`);
  assertOnlyKeys(release, ['version', 'lineName', 'releaseNotes', 'riskLevel', 'environmentConstraints', 'parameters', 'dependencies', 'actions'], `第 ${index + 1} 项 release`);
  const slug = nonEmptyString(component.slug, `第 ${index + 1} 项 component.slug`);
  if (!SLUG.test(slug)) throw new Error(`${slug} 的 slug 只能包含小写字母、数字和连字符。`);
  const layer = nonEmptyString(component.layer, `${slug}.component.layer`) as Component['layer'];
  const layerDefinition = COMPONENT_LAYERS.find((item) => item.value === layer);
  if (!layerDefinition) throw new Error(`${slug} 的组件分层无效。`);
  const tags = stringArray(component.tags, `${slug}.component.tags`);
  if (tags.length > 8) throw new Error(`${slug} 最多允许 8 个标签。`);
  if (tags.some((tag) => tag.length > 32 || tag !== tag.toLowerCase() || /[\s,]/.test(tag))) throw new Error(`${slug} 的标签必须为不超过 32 字符的小写非空白文本。`);
  const version = nonEmptyString(release.version, `${slug}.release.version`);
  const lineName = nonEmptyString(release.lineName, `${slug}.release.lineName`);
  if (release.environmentConstraints !== undefined && !isRecord(release.environmentConstraints)) {
    throw new Error(`${slug}.release.environmentConstraints 必须是对象。`);
  }
  const sensitiveConstraint = findSensitivePath(release.environmentConstraints ?? {});
  if (sensitiveConstraint) throw new Error(`${slug}.release.environmentConstraints.${sensitiveConstraint} 必须改用 CredentialRef。`);
  const releaseNotes = optionalString(release.releaseNotes, `${slug}.release.releaseNotes`) ?? '';
  const riskLevel = (optionalString(release.riskLevel, `${slug}.release.riskLevel`) ?? 'low') as NonNullable<ComponentRelease['riskLevel']>;
  if (!RISK_LEVELS.has(riskLevel)) throw new Error(`${slug}.release.riskLevel 无效。`);

  const parameters = optionalArray(release.parameters, `${slug}.release.parameters`).map((raw, parameterIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${parameterIndex + 1} 个参数必须是对象。`);
    assertOnlyKeys(raw, ['name', 'description', 'type', 'required', 'defaultValue', 'visibility', 'enum', 'minLength'], `${slug}.parameters[${parameterIndex}]`);
    const name = nonEmptyString(raw.name, `${slug}.parameters[${parameterIndex}].name`);
    if (isSensitiveKey(name)) throw new Error(`${slug}.${name} 必须改用 CredentialRef。`);
    const description = nonEmptyString(raw.description, `${slug}.${name}.description`);
    const type = nonEmptyString(raw.type, `${slug}.${name}.type`) as ParameterDefinition['type'];
    if (!PARAMETER_TYPES.has(type)) throw new Error(`${slug}.${name} 的参数类型无效。`);
    if (raw.visibility !== 'internal' && raw.visibility !== 'public') throw new Error(`${slug}.${name} 必须声明 internal 或 public。`);
    const required = optionalBoolean(raw.required, `${slug}.${name}.required`);
    const enumValues = optionalArray(raw.enum, `${slug}.${name}.enum`);
    if (enumValues.some((item) => !matchesParameterType(item, type))) throw new Error(`${slug}.${name}.enum 包含与 ${type} 不匹配的值。`);
    let minLength: number | undefined;
    if (raw.minLength !== undefined) {
      if (typeof raw.minLength !== 'number' || !Number.isInteger(raw.minLength) || raw.minLength < 0) throw new Error(`${slug}.${name}.minLength 必须是非负整数。`);
      if (type !== 'string') throw new Error(`${slug}.${name}.minLength 只适用于 string。`);
      minLength = raw.minLength;
    }
    const hasDefault = raw.defaultValue !== undefined && raw.defaultValue !== null;
    if (hasDefault && !matchesParameterType(raw.defaultValue, type)) throw new Error(`${slug}.${name}.defaultValue 必须匹配 ${type}。`);
    const sensitiveDefault = hasDefault ? findSensitivePath(raw.defaultValue, name) : undefined;
    if (sensitiveDefault) throw new Error(`${slug}.${sensitiveDefault} 的默认值必须改用 CredentialRef。`);
    if (hasDefault && minLength && [...(raw.defaultValue as string)].length < minLength) throw new Error(`${slug}.${name}.defaultValue 短于 minLength。`);
    if (hasDefault && enumValues.length && !enumValues.some((item) => parameterValuesEqual(item, raw.defaultValue))) throw new Error(`${slug}.${name}.defaultValue 不在 enum 中。`);
    return {
      name, description, type, visibility: raw.visibility, required,
      ...(hasDefault ? { defaultValue: raw.defaultValue } : {}),
      ...(raw.enum === undefined ? {} : { enum: enumValues }),
      ...(minLength === undefined ? {} : { minLength }),
    } as ParameterDefinition;
  });
  if (new Set(parameters.map((item) => item.name)).size !== parameters.length) throw new Error(`${slug} 包含重复参数名。`);

  const dependencies = optionalArray(release.dependencies, `${slug}.release.dependencies`).map((raw, dependencyIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${dependencyIndex + 1} 个依赖必须是对象。`);
    assertOnlyKeys(raw, ['componentSlug', 'purpose', 'parameterMappings'], `${slug}.dependencies[${dependencyIndex}]`);
    const componentSlug = nonEmptyString(raw.componentSlug, `${slug}.dependencies[${dependencyIndex}].componentSlug`);
    const parameterMappings = optionalArray(raw.parameterMappings, `${slug}.${componentSlug}.parameterMappings`).map((mapping, mappingIndex) => {
      if (!isRecord(mapping)) throw new Error(`${slug}.${componentSlug} 的第 ${mappingIndex + 1} 个参数映射必须是对象。`);
      assertOnlyKeys(mapping, ['upstreamParameter', 'targetParameter'], `${slug}.${componentSlug}.parameterMappings[${mappingIndex}]`);
      const upstreamParameter = nonEmptyString(mapping.upstreamParameter, `${slug}.${componentSlug}.upstreamParameter`);
      const targetParameter = nonEmptyString(mapping.targetParameter, `${slug}.${componentSlug}.targetParameter`);
      if (isSensitiveKey(upstreamParameter) || isSensitiveKey(targetParameter)) throw new Error(`${slug}.${componentSlug} 的敏感参数映射必须改用 CredentialRef。`);
      return { upstreamParameter, targetParameter };
    });
    const purpose = optionalString(raw.purpose, `${slug}.${componentSlug}.purpose`) ?? '';
    return { componentSlug, purpose, parameterMappings };
  });
  if (new Set(dependencies.map((item) => item.componentSlug)).size !== dependencies.length) throw new Error(`${slug} 包含重复组件依赖。`);

  const actions = optionalArray(release.actions, `${slug}.release.actions`).map((raw, actionIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${actionIndex + 1} 个 Action 必须是对象。`);
    assertOnlyKeys(raw, ['name', 'type', 'playbook', 'tags', 'limit', 'hostGroup', 'timeoutSeconds', 'allowedParameters', 'requiredCredentials', 'riskLevel', 'destructive', 'idempotent', 'fromReleaseId', 'toReleaseId'], `${slug}.actions[${actionIndex}]`);
    const type = nonEmptyString(raw.type, `${slug}.actions[${actionIndex}].type`) as ActionDefinition['type'];
    if (!ACTION_TYPES.has(type)) throw new Error(`${slug} 的 Action 类型 ${type} 无效。`);
    if (type === 'upgrade') throw new Error(`${slug} 的导入模板不能声明显式 upgrade；请使用幂等 install，或创建后在前台绑定既有 Release ID。`);
    const playbook = nonEmptyString(raw.playbook, `${slug}.actions[${actionIndex}].playbook`);
    if (!PLAYBOOK_FILENAME.test(playbook)) throw new Error(`${slug} 的 Action Playbook 必须填写模板内的文件名。`);
    const name = optionalString(raw.name, `${slug}.actions[${actionIndex}].name`) ?? '';
    const tags = stringArray(raw.tags, `${slug}.${type}.tags`);
    const allowedParameters = stringArray(raw.allowedParameters, `${slug}.${type}.allowedParameters`);
    if (allowedParameters.some(isSensitiveKey)) throw new Error(`${slug}.${type}.allowedParameters 的敏感参数必须改用 CredentialRef。`);
    const requiredCredentials = stringArray(raw.requiredCredentials, `${slug}.${type}.requiredCredentials`);
    const timeoutSeconds = raw.timeoutSeconds === undefined ? 1800 : raw.timeoutSeconds;
    if (typeof timeoutSeconds !== 'number' || !Number.isInteger(timeoutSeconds) || timeoutSeconds <= 0) throw new Error(`${slug}.${type}.timeoutSeconds 必须是正整数。`);
    const actionRisk = (optionalString(raw.riskLevel, `${slug}.${type}.riskLevel`) ?? 'low') as NonNullable<ActionDefinition['riskLevel']>;
    if (!RISK_LEVELS.has(actionRisk)) throw new Error(`${slug}.${type}.riskLevel 无效。`);
    const destructive = optionalBoolean(raw.destructive, `${slug}.${type}.destructive`, actionRisk === 'destructive');
    const idempotent = optionalBoolean(raw.idempotent, `${slug}.${type}.idempotent`);
    if (idempotent && type !== 'install') throw new Error(`${slug} 只有 install Action 可以声明 idempotent。`);
    const fromReleaseId = optionalString(raw.fromReleaseId, `${slug}.${type}.fromReleaseId`) ?? '';
    const toReleaseId = optionalString(raw.toReleaseId, `${slug}.${type}.toReleaseId`) ?? '';
    if (type === 'rollback' && (fromReleaseId || toReleaseId)) throw new Error(`${slug} 的导入模板只支持不绑定 Release ID 的安装回退。`);
    if (type !== 'rollback' && (fromReleaseId || toReleaseId)) throw new Error(`${slug}.${type} 不能携带 fromReleaseId/toReleaseId。`);
    return {
      name, type, playbook, tags,
      limit: optionalString(raw.limit, `${slug}.${type}.limit`) ?? '',
      hostGroup: optionalString(raw.hostGroup, `${slug}.${type}.hostGroup`) ?? '',
      timeoutSeconds, allowedParameters, requiredCredentials,
      riskLevel: actionRisk, destructive, idempotent,
      ...(fromReleaseId ? { fromReleaseId } : {}),
      ...(toReleaseId ? { toReleaseId } : {}),
    };
  });
  if (new Set(actions.map((action) => action.type)).size !== actions.length) throw new Error(`${slug} 包含重复 Action 类型。`);
  const parameterNames = new Set(parameters.map((parameter) => parameter.name));
  for (const action of actions) {
    for (const name of action.allowedParameters ?? []) if (!parameterNames.has(name)) throw new Error(`${slug}.${action.type} 引用了未声明参数 ${name}。`);
  }

  const playbooks = optionalArray(value.playbooks, `${slug}.playbooks`).map((raw, playbookIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${playbookIndex + 1} 个 Playbook 必须是对象。`);
    assertOnlyKeys(raw, ['filename', 'content'], `${slug}.playbooks[${playbookIndex}]`);
    const filename = nonEmptyString(raw.filename, `${slug}.playbooks[${playbookIndex}].filename`);
    if (!PLAYBOOK_FILENAME.test(filename)) throw new Error(`${slug} 的 Playbook 文件名 ${filename} 无效。`);
    if (typeof raw.content !== 'string' || !raw.content.trim()) throw new Error(`${slug}.${filename}.content不能为空。`);
    const content = raw.content;
    if (new TextEncoder().encode(content).length > MAX_PLAYBOOK_BYTES) throw new Error(`${slug}/${filename} 超过 1 MiB。`);
    return { filename, content };
  });
  const filenames = playbooks.map((item) => item.filename);
  if (new Set(filenames).size !== filenames.length) throw new Error(`${slug} 包含重复 Playbook 文件名。`);
  const referenced = new Set(actions.map((action) => action.playbook));
  for (const filename of referenced) if (!filenames.includes(filename)) throw new Error(`${slug} 的 Action 引用了未提供的 Playbook ${filename}。`);
  for (const filename of filenames) if (!referenced.has(filename)) throw new Error(`${slug} 的 Playbook ${filename} 未被任何 Action 引用。`);

  return {
    component: {
      name: nonEmptyString(component.name, `${slug}.component.name`), slug,
      description: optionalString(component.description, `${slug}.component.description`) ?? '',
      layer, tags,
    },
    release: {
      version,
      lineName,
      releaseNotes,
      riskLevel,
      environmentConstraints: (release.environmentConstraints ?? {}) as Record<string, unknown>,
      parameters,
      dependencies,
      actions,
    } as ComponentImportEntry['release'],
    playbooks,
  };
}

function validateDependencyGraph(entries: ComponentImportEntry[]) {
  const slugs = new Set(entries.map((entry) => entry.component.slug));
  for (const entry of entries) {
    for (const dependency of entry.release.dependencies ?? []) {
      if (!slugs.has(dependency.componentSlug)) throw new Error(`${entry.component.slug} 引用了模板外组件 ${dependency.componentSlug}。`);
      if (dependency.componentSlug === entry.component.slug) throw new Error(`${entry.component.slug} 不能依赖自身。`);
    }
  }
  const remaining = new Map(entries.map((entry) => [entry.component.slug, new Set((entry.release.dependencies ?? []).map((dependency) => dependency.componentSlug))]));
  while (remaining.size) {
    const ready = [...remaining].filter(([, dependencies]) => [...dependencies].every((slug) => !remaining.has(slug))).map(([slug]) => slug);
    if (!ready.length) throw new Error('组件依赖存在环，无法确定 Release 创建顺序。');
    for (const slug of ready) remaining.delete(slug);
  }
}

function validateDependencyContracts(entries: ComponentImportEntry[]) {
  const bySlug = new Map(entries.map((entry) => [entry.component.slug, entry]));
  for (const entry of entries) {
    const targets = new Map((entry.release.parameters ?? []).map((parameter) => [parameter.name, parameter]));
    const mappedTargets = new Set<string>();
    for (const dependency of entry.release.dependencies ?? []) {
      const upstream = bySlug.get(dependency.componentSlug)!;
      const sources = new Map((upstream.release.parameters ?? []).map((parameter) => [parameter.name, parameter]));
      for (const mapping of dependency.parameterMappings ?? []) {
        const target = targets.get(mapping.targetParameter);
        if (!target) throw new Error(`${entry.component.slug} 的映射目标 ${mapping.targetParameter} 未声明。`);
        if (mappedTargets.has(mapping.targetParameter)) throw new Error(`${entry.component.slug} 的参数 ${mapping.targetParameter} 被重复映射。`);
        mappedTargets.add(mapping.targetParameter);
        const source = sources.get(mapping.upstreamParameter);
        if (!source || source.visibility !== 'public') throw new Error(`${entry.component.slug} 的映射源 ${dependency.componentSlug}.${mapping.upstreamParameter} 必须是公开参数。`);
        if (source.type !== target.type) throw new Error(`${entry.component.slug}.${mapping.targetParameter} 与上游 ${mapping.upstreamParameter} 类型不一致。`);
      }
    }
  }
}

export function parseComponentImportTemplate(text: string): ComponentImportEntry[] {
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new Error('组件模板不是有效 JSON。'); }
  if (!Array.isArray(raw) || !raw.length || raw.length > 50) throw new Error('模板必须包含 1 到 50 个组件。');
  const entries = raw.map(parseEntry);
  const slugs = entries.map((entry) => entry.component.slug);
  if (new Set(slugs).size !== slugs.length) throw new Error('每个组件必须有唯一 slug。');
  validateDependencyGraph(entries);
  validateDependencyContracts(entries);
  return entries;
}
