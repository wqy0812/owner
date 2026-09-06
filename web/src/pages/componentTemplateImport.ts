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

const ROLE_PATH=/^(tasks|templates|files|handlers|defaults|vars)\/[A-Za-z0-9_-][A-Za-z0-9_./-]*$/;
const ACTION_TYPES = new Set<ActionDefinition['type']>(['check', 'install', 'configure', 'upgrade', 'rollback', 'uninstall']);
const PARAMETER_TYPES = new Set<ParameterDefinition['type']>(['string', 'integer', 'number', 'boolean', 'object', 'array']);
const RISK_LEVELS = new Set<NonNullable<ComponentRelease['riskLevel']>>(['low', 'medium', 'high', 'destructive']);
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

function parseResourceContract(value: unknown): ActionDefinition['resourceContract'] {
  if(value===undefined || value===null)return undefined;
  if(!isRecord(value) || value.version!==1 || typeof value.noManagedPaths!=='boolean' || !Array.isArray(value.claims))throw new Error('资源声明必须包含 version: 1、noManagedPaths 和 claims。');
  assertOnlyKeys(value,['version','noManagedPaths','claims'],'资源声明');
  const claims=value.claims.map((claim,i)=>{
    if(!isRecord(claim))throw new Error(`资源声明 ${i+1} 必须是对象。`);
    assertOnlyKeys(claim,['id','path','scope','access','exclusive','excludes','sharedPaths','sharedWith'],'资源路径');
    if(claim.scope!=='file' && claim.scope!=='tree')throw new Error('资源范围必须为 file 或 tree。');
    if(claim.access!=='manage' && claim.access!=='read' && claim.access!=='verify')throw new Error('资源用途无效。');
    let sharedWith;
    if(claim.sharedWith!==undefined){if(!isRecord(claim.sharedWith))throw new Error('共享来源必须明确版本及资源。');sharedWith={releaseId:nonEmptyString(claim.sharedWith.releaseId,'共享版本'),claimId:nonEmptyString(claim.sharedWith.claimId,'共享资源')};}
    return {id:nonEmptyString(claim.id,'资源 ID'),path:nonEmptyString(claim.path,'资源路径'),scope:claim.scope,access:claim.access,exclusive:optionalBoolean(claim.exclusive,'独占范围'),excludes:stringArray(claim.excludes,'排除路径'),sharedPaths:stringArray(claim.sharedPaths,'共享子路径'),sharedWith};
  });
  if(value.noManagedPaths===Boolean(claims.length))throw new Error('请填写资源声明，或明确确认无受管路径。');
  return {version:1,noManagedPaths:value.noManagedPaths,claims:claims as NonNullable<ActionDefinition['resourceContract']>['claims']};
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
    assertOnlyKeys(raw, ['name', 'description', 'type', 'required', 'visibility', 'modifiable', 'valueProvider', 'fixedValue', 'suggestedValue', 'testValue', 'environmentBinding', 'enum', 'minLength'], `${slug}.parameters[${parameterIndex}]`);
    const name = nonEmptyString(raw.name, `${slug}.parameters[${parameterIndex}].name`);
    if (isSensitiveKey(name)) throw new Error(`${slug}.${name} 必须改用 CredentialRef。`);
    const description = nonEmptyString(raw.description, `${slug}.${name}.description`);
    const type = nonEmptyString(raw.type, `${slug}.${name}.type`) as ParameterDefinition['type'];
    if (!PARAMETER_TYPES.has(type)) throw new Error(`${slug}.${name} 的参数类型无效。`);
    if (raw.visibility !== 'internal' && raw.visibility !== 'public') throw new Error(`${slug}.${name} 必须声明 internal 或 public。`);
    const required = optionalBoolean(raw.required, `${slug}.${name}.required`);
    const modifiable = optionalBoolean(raw.modifiable, `${slug}.${name}.modifiable`);
    const valueProvider = nonEmptyString(raw.valueProvider, `${slug}.${name}.valueProvider`) as ParameterDefinition['valueProvider'];
    if (!['component_owner', 'scenario_owner', 'environment_owner', 'upstream_mapping'].includes(valueProvider)) throw new Error(`${slug}.${name}.valueProvider 无效。`);
    const enumValues = optionalArray(raw.enum, `${slug}.${name}.enum`);
    if (enumValues.some((item) => !matchesParameterType(item, type))) throw new Error(`${slug}.${name}.enum 包含与 ${type} 不匹配的值。`);
    let minLength: number | undefined;
    if (raw.minLength !== undefined) {
      if (typeof raw.minLength !== 'number' || !Number.isInteger(raw.minLength) || raw.minLength < 0) throw new Error(`${slug}.${name}.minLength 必须是非负整数。`);
      if (type !== 'string') throw new Error(`${slug}.${name}.minLength 只适用于 string。`);
      minLength = raw.minLength;
    }
    for (const field of ['fixedValue', 'suggestedValue', 'testValue'] as const) {
      const value = raw[field];
      if (value === undefined || value === null) continue;
      if (!matchesParameterType(value, type)) throw new Error(`${slug}.${name}.${field} 必须匹配 ${type}。`);
      if (findSensitivePath(value, name)) throw new Error(`${slug}.${name}.${field} 必须改用 CredentialRef。`);
      if (minLength && [...(value as string)].length < minLength) throw new Error(`${slug}.${name}.${field} 短于 minLength。`);
      if (enumValues.length && !enumValues.some((item) => parameterValuesEqual(item, value))) throw new Error(`${slug}.${name}.${field} 不在 enum 中。`);
    }
    if (valueProvider === 'component_owner' && (modifiable || raw.fixedValue === undefined)) throw new Error(`${slug}.${name} 的组件固定参数必须不可修改且填写 fixedValue。`);
    if ((valueProvider === 'scenario_owner' || valueProvider === 'environment_owner') && !modifiable) throw new Error(`${slug}.${name} 的外部 Owner 参数必须允许修改。`);
    if ((valueProvider === 'scenario_owner' || valueProvider === 'environment_owner') && (type === 'object' || type === 'array') && !enumValues.length) throw new Error(`${slug}.${name} 是结构化外部字段，必须提供受控枚举，不能编辑原始 JSON。`);
    if (valueProvider === 'upstream_mapping' && modifiable) throw new Error(`${slug}.${name} 的上游映射参数不能人工修改。`);
    if (isRecord(raw.environmentBinding) && raw.environmentBinding.kind === 'global') throw new Error(`${slug}.${name} 使用了已停用的全局字段，请改为组件参数或上游映射。`);
    if (valueProvider === 'environment_owner' && !isRecord(raw.environmentBinding)) throw new Error(`${slug}.${name} 必须声明 environmentBinding。`);
    return {
      name, description, type, visibility: raw.visibility, required, modifiable, valueProvider,
      ...(raw.fixedValue === undefined ? {} : { fixedValue: raw.fixedValue }),
      ...(raw.suggestedValue === undefined ? {} : { suggestedValue: raw.suggestedValue }),
      ...(raw.testValue === undefined ? {} : { testValue: raw.testValue }),
      ...(raw.environmentBinding === undefined ? {} : { environmentBinding: raw.environmentBinding as ParameterDefinition['environmentBinding'] }),
      ...(raw.enum === undefined ? {} : { enum: enumValues }),
      ...(minLength === undefined ? {} : { minLength }),
    } as ParameterDefinition;
  });
  if (new Set(parameters.map((item) => item.name)).size !== parameters.length) throw new Error(`${slug} 包含重复参数名。`);

  const dependencies = optionalArray(release.dependencies, `${slug}.release.dependencies`).map((raw, dependencyIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${dependencyIndex + 1} 个依赖必须是对象。`);
    assertOnlyKeys(raw, ['componentSlug', 'purpose', 'parameterMappings', 'kind'], `${slug}.dependencies[${dependencyIndex}]`);
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
    if (raw.kind !== undefined && raw.kind !== '' && raw.kind !== 'configuration') throw new Error(`${slug} 的引用方式无效。`);
    if (raw.kind === 'configuration' && !parameterMappings.length) throw new Error(`${slug} 的配置引用必须指定参数。`);
    return { componentSlug, purpose, parameterMappings, ...(raw.kind === 'configuration' ? { kind: 'configuration' as const } : {}) };
  });
  if (new Set(dependencies.map((item) => item.componentSlug)).size !== dependencies.length) throw new Error(`${slug} 包含重复组件依赖。`);

  const actions = optionalArray(release.actions, `${slug}.release.actions`).map((raw, actionIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${actionIndex + 1} 个 Action 必须是对象。`);
    assertOnlyKeys(raw, ['resourceContract','id','preCheckActionId','postCheckActionId','become','name', 'type', 'playbook', 'tags', 'hostGroup', 'timeoutSeconds', 'requiredCredentials', 'riskLevel', 'destructive', 'idempotent', 'fromReleaseId', 'toReleaseId'], `${slug}.actions[${actionIndex}]`);
    const type = nonEmptyString(raw.type, `${slug}.actions[${actionIndex}].type`) as ActionDefinition['type'];
    if (!ACTION_TYPES.has(type)) throw new Error(`${slug} 的 Action 类型 ${type} 无效。`);
    if (type === 'upgrade') throw new Error(`${slug} 的导入模板不能声明显式 upgrade；请使用幂等 install，或创建后在前台绑定既有 Release ID。`);
    const playbook = nonEmptyString(raw.playbook, `${slug}.actions[${actionIndex}].playbook`);
    if ((!ROLE_PATH.test(playbook)||playbook.split('/').some(part=>part==='..'||part==='.'))) throw new Error(`${slug} 的 Action Playbook 必须填写模板内的文件名。`);
    const name = optionalString(raw.name, `${slug}.actions[${actionIndex}].name`) ?? '';
    const tags = stringArray(raw.tags, `${slug}.${type}.tags`);
    const requiredCredentials = stringArray(raw.requiredCredentials, `${slug}.${type}.requiredCredentials`);
    const timeoutSeconds = raw.timeoutSeconds === undefined ? 1800 : raw.timeoutSeconds;
    if (typeof timeoutSeconds !== 'number' || !Number.isInteger(timeoutSeconds) || timeoutSeconds <= 0) throw new Error(`${slug}.${type}.timeoutSeconds 必须是正整数。`);
    const actionRisk = (optionalString(raw.riskLevel, `${slug}.${type}.riskLevel`) ?? 'low') as NonNullable<ActionDefinition['riskLevel']>;
    if (!RISK_LEVELS.has(actionRisk)) throw new Error(`${slug}.${type}.riskLevel 无效。`);
    const destructive = optionalBoolean(raw.destructive, `${slug}.${type}.destructive`, actionRisk === 'destructive');
    const idempotent = optionalBoolean(raw.idempotent, `${slug}.${type}.idempotent`);
    if (idempotent && type === 'check') throw new Error(`${slug} 检查动作不能声明可安全重试。`);
    const fromReleaseId = optionalString(raw.fromReleaseId, `${slug}.${type}.fromReleaseId`) ?? '';
    const toReleaseId = optionalString(raw.toReleaseId, `${slug}.${type}.toReleaseId`) ?? '';
    if (type === 'rollback' && (fromReleaseId || toReleaseId)) throw new Error(`${slug} 的导入模板只支持不绑定 Release ID 的安装回退。`);
    if (type !== 'rollback' && (fromReleaseId || toReleaseId)) throw new Error(`${slug}.${type} 不能携带 fromReleaseId/toReleaseId。`);
    return {
      id:nonEmptyString(raw.id,`${slug}.actions[${actionIndex}].id`),preCheckActionId:optionalString(raw.preCheckActionId,'preCheckActionId'),postCheckActionId:optionalString(raw.postCheckActionId,'postCheckActionId'),become:optionalBoolean(raw.become,'become')??false,name, type, playbook, tags,
      hostGroup: optionalString(raw.hostGroup, `${slug}.${type}.hostGroup`) ?? '',
      timeoutSeconds, requiredCredentials,
      riskLevel: actionRisk, destructive, idempotent,
      resourceContract: parseResourceContract(raw.resourceContract),
      ...(fromReleaseId ? { fromReleaseId } : {}),
      ...(toReleaseId ? { toReleaseId } : {}),
    };
  });
  if (new Set(actions.filter(action=>action.type!=='check').map((action) => action.type)).size !== actions.filter(action=>action.type!=='check').length) throw new Error(`${slug} 包含重复 Action 类型。`);

  const playbooks = optionalArray(value.playbooks, `${slug}.playbooks`).map((raw, playbookIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${playbookIndex + 1} 个 Playbook 必须是对象。`);
    assertOnlyKeys(raw, ['filename', 'content'], `${slug}.playbooks[${playbookIndex}]`);
    const filename = nonEmptyString(raw.filename, `${slug}.playbooks[${playbookIndex}].filename`);
    if ((!ROLE_PATH.test(filename)||filename.split('/').some(part=>part==='..'||part==='.'))) throw new Error(`${slug} 的 Playbook 文件名 ${filename} 无效。`);
    if (typeof raw.content !== 'string' || !raw.content.trim()) throw new Error(`${slug}.${filename}.content不能为空。`);
    const content = raw.content;
    if (new TextEncoder().encode(content).length > MAX_PLAYBOOK_BYTES) throw new Error(`${slug}/${filename} 超过 1 MiB。`);
    return { filename, content };
  });
  const filenames = playbooks.map((item) => item.filename);
  if (new Set(filenames).size !== filenames.length) throw new Error(`${slug} 包含重复 Playbook 文件名。`);
  const referenced = new Set(actions.map((action) => action.playbook));
  for (const filename of referenced) if (!filenames.includes(filename)) throw new Error(`${slug} 的 Action 引用了未提供的 Playbook ${filename}。`);


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
  const remaining = new Map(entries.map((entry) => [entry.component.slug, new Set((entry.release.dependencies ?? []).filter((dependency) => dependency.kind !== 'configuration').map((dependency) => dependency.componentSlug))]));
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
        if (target.valueProvider !== 'upstream_mapping') throw new Error(`${entry.component.slug}.${mapping.targetParameter} 必须由 upstream_mapping 提供。`);
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
