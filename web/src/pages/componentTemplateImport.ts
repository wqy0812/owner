import { COMPONENT_LAYERS } from '../types/componentClassification';
import type {
  ActionDefinition,
  Component,
  ComponentDependency,
  ComponentRelease,
  ParameterDefinition,
  PlaybookFile,
} from '../types/domain';

export type ComponentImportDependency = Omit<ComponentDependency, 'componentId' | 'releaseId'> & { componentSlug: string };

export type ComponentImportEntry = {
  component: Pick<Component, 'name' | 'layer' | 'category' | 'kind' | 'requiredness'> & { slug: string; description?: string };
  release: Omit<Partial<ComponentRelease>, 'dependencies'> & { dependencies?: ComponentImportDependency[] };
  playbooks?: Array<{ filename: string; content: string }>;
};

export interface ComponentImportClient {
  createComponent(input: Partial<Component>): Promise<Component>;
  createRelease(componentId: string, input: Partial<ComponentRelease>): Promise<ComponentRelease>;
  savePlaybook(releaseId: string, filename: string, content: string): Promise<PlaybookFile>;
  updateRelease(releaseId: string, input: Partial<ComponentRelease>): Promise<ComponentRelease>;
}

export interface ComponentImportProgress {
  stage: string;
  completedComponents: string[];
  completedReleases: string[];
}

export class ComponentImportExecutionError extends Error {
  readonly progress: ComponentImportProgress;

  constructor(message: string, progress: ComponentImportProgress) {
    super(message);
    this.name = 'ComponentImportExecutionError';
    this.progress = progress;
  }
}

const ACTION_TYPES = new Set<ActionDefinition['type']>(['inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall']);
const COMPONENT_KINDS = new Set<Component['kind']>(['software', 'software_bundle', 'delivery_stage', 'configuration', 'artifact_set']);
const REQUIREDNESS = new Set<Component['requiredness']>(['core_required', 'profile_required', 'optional']);
const RELEASE_TYPES = new Set<NonNullable<ComponentRelease['type']>>(['atomic', 'bundle']);
const PARAMETER_TYPES = new Set<ParameterDefinition['type']>(['string', 'integer', 'number', 'boolean', 'object', 'array']);
const PLAYBOOK_FILENAME = /^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:yml|yaml)$/;
const SLUG = /^[a-z0-9][a-z0-9-]*$/;
const MAX_PLAYBOOK_BYTES = 1 << 20;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function nonEmptyString(value: unknown, label: string): string {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${label}不能为空。`);
  return value.trim();
}

function optionalArray(value: unknown, label: string): unknown[] {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error(`${label}必须是数组。`);
  return value;
}

function parseEntry(value: unknown, index: number): ComponentImportEntry {
  if (!isRecord(value) || !isRecord(value.component) || !isRecord(value.release)) {
    throw new Error(`第 ${index + 1} 项必须包含 component 和 release 对象。`);
  }
  const component = value.component;
  const release = value.release;
  const slug = nonEmptyString(component.slug, `第 ${index + 1} 项 component.slug`);
  if (!SLUG.test(slug)) throw new Error(`${slug} 的 slug 只能包含小写字母、数字和连字符。`);
  const layer = nonEmptyString(component.layer, `${slug}.component.layer`) as Component['layer'];
  const layerDefinition = COMPONENT_LAYERS.find((item) => item.value === layer);
  if (!layerDefinition) throw new Error(`${slug} 的组件分层无效。`);
  const category = nonEmptyString(component.category, `${slug}.component.category`) as Component['category'];
  if (!layerDefinition.categories.includes(category)) throw new Error(`${slug} 的 category 不属于所选分层。`);
  const kind = nonEmptyString(component.kind, `${slug}.component.kind`) as Component['kind'];
  if (!COMPONENT_KINDS.has(kind)) throw new Error(`${slug} 的组件类型无效。`);
  const requiredness = nonEmptyString(component.requiredness, `${slug}.component.requiredness`) as Component['requiredness'];
  if (!REQUIREDNESS.has(requiredness)) throw new Error(`${slug} 的必选级别无效。`);
  const version = nonEmptyString(release.version, `${slug}.release.version`);
  const releaseType = nonEmptyString(release.type, `${slug}.release.type`) as NonNullable<ComponentRelease['type']>;
  if (!RELEASE_TYPES.has(releaseType)) throw new Error(`${slug} 的 Release 类型无效。`);
  if (release.environmentConstraints !== undefined && !isRecord(release.environmentConstraints)) {
    throw new Error(`${slug}.release.environmentConstraints 必须是对象。`);
  }

  const parameters = optionalArray(release.parameters, `${slug}.release.parameters`).map((raw, parameterIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${parameterIndex + 1} 个参数必须是对象。`);
    const name = nonEmptyString(raw.name, `${slug}.parameters[${parameterIndex}].name`);
    const description = nonEmptyString(raw.description, `${slug}.${name}.description`);
    const type = nonEmptyString(raw.type, `${slug}.${name}.type`) as ParameterDefinition['type'];
    if (!PARAMETER_TYPES.has(type)) throw new Error(`${slug}.${name} 的参数类型无效。`);
    if (raw.visibility !== 'internal' && raw.visibility !== 'public') throw new Error(`${slug}.${name} 必须声明 internal 或 public。`);
    return { ...raw, name, description, type, visibility: raw.visibility } as ParameterDefinition;
  });
  if (new Set(parameters.map((item) => item.name)).size !== parameters.length) throw new Error(`${slug} 包含重复参数名。`);

  const dependencies = optionalArray(release.dependencies, `${slug}.release.dependencies`).map((raw, dependencyIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${dependencyIndex + 1} 个依赖必须是对象。`);
    const componentSlug = nonEmptyString(raw.componentSlug, `${slug}.dependencies[${dependencyIndex}].componentSlug`);
    const parameterMappings = optionalArray(raw.parameterMappings, `${slug}.${componentSlug}.parameterMappings`).map((mapping, mappingIndex) => {
      if (!isRecord(mapping)) throw new Error(`${slug}.${componentSlug} 的第 ${mappingIndex + 1} 个参数映射必须是对象。`);
      return {
        upstreamParameter: nonEmptyString(mapping.upstreamParameter, `${slug}.${componentSlug}.upstreamParameter`),
        targetParameter: nonEmptyString(mapping.targetParameter, `${slug}.${componentSlug}.targetParameter`),
      };
    });
    return { componentSlug, purpose: typeof raw.purpose === 'string' ? raw.purpose : '', parameterMappings };
  });
  if (new Set(dependencies.map((item) => item.componentSlug)).size !== dependencies.length) throw new Error(`${slug} 包含重复组件依赖。`);

  const actions = optionalArray(release.actions, `${slug}.release.actions`).map((raw, actionIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${actionIndex + 1} 个 Action 必须是对象。`);
    const type = nonEmptyString(raw.type, `${slug}.actions[${actionIndex}].type`) as ActionDefinition['type'];
    if (!ACTION_TYPES.has(type)) throw new Error(`${slug} 的 Action 类型 ${type} 无效。`);
    const playbook = nonEmptyString(raw.playbook, `${slug}.actions[${actionIndex}].playbook`);
    if (!PLAYBOOK_FILENAME.test(playbook)) throw new Error(`${slug} 的 Action Playbook 必须填写模板内的文件名。`);
    if (raw.idempotent === true && type !== 'install') throw new Error(`${slug} 只有 install Action 可以声明 idempotent。`);
    return { ...raw, type, playbook } as ActionDefinition;
  });

  const playbooks = optionalArray(value.playbooks, `${slug}.playbooks`).map((raw, playbookIndex) => {
    if (!isRecord(raw)) throw new Error(`${slug} 的第 ${playbookIndex + 1} 个 Playbook 必须是对象。`);
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
      description: typeof component.description === 'string' ? component.description : '',
      layer, category, kind, requiredness,
    },
    release: {
      ...release,
      version,
      type: releaseType,
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

export function parseComponentImportTemplate(text: string): ComponentImportEntry[] {
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new Error('组件模板不是有效 JSON。'); }
  if (!Array.isArray(raw) || !raw.length || raw.length > 50) throw new Error('模板必须包含 1 到 50 个组件。');
  const entries = raw.map(parseEntry);
  const slugs = entries.map((entry) => entry.component.slug);
  if (new Set(slugs).size !== slugs.length) throw new Error('每个组件必须有唯一 slug。');
  validateDependencyGraph(entries);
  return entries;
}

function mappedDependencies(entry: ComponentImportEntry, components: Map<string, Component>, releases: Map<string, ComponentRelease>): ComponentDependency[] {
  return (entry.release.dependencies ?? []).map((dependency) => ({
    ...dependency,
    componentId: components.get(dependency.componentSlug)!.id,
    releaseId: releases.get(dependency.componentSlug)!.id,
  }));
}

export async function executeComponentImport(
  entries: ComponentImportEntry[],
  client: ComponentImportClient,
  onProgress: (progress: ComponentImportProgress) => void = () => undefined,
) {
  const components = new Map<string, Component>();
  const releases = new Map<string, ComponentRelease>();
  const completedComponents: string[] = [];
  const completedReleases: string[] = [];
  let stage = '准备导入';
  const progress = () => ({ stage, completedComponents: [...completedComponents], completedReleases: [...completedReleases] });
  try {
    for (const [index, entry] of entries.entries()) {
      stage = `创建组件 ${index + 1}/${entries.length}：${entry.component.name}`;
      onProgress(progress());
      components.set(entry.component.slug, await client.createComponent(entry.component));
      completedComponents.push(entry.component.slug);
    }
    const pending = [...entries];
    while (pending.length) {
      const readyIndex = pending.findIndex((entry) => (entry.release.dependencies ?? []).every((dependency) => releases.has(dependency.componentSlug)));
      if (readyIndex < 0) throw new Error('组件依赖顺序在写入期间失效。');
      const [entry] = pending.splice(readyIndex, 1);
      const component = components.get(entry.component.slug)!;
      const dependencies = mappedDependencies(entry, components, releases);
      const desiredActions = entry.release.actions ?? [];
      stage = `创建安全 Draft：${entry.component.name} ${entry.release.version ?? ''}`;
      onProgress(progress());
      let release = await client.createRelease(component.id, { ...entry.release, dependencies, actions: [] });
      const managedPaths = new Map<string, string>();
      for (const playbook of entry.playbooks ?? []) {
        stage = `保存 Playbook：${entry.component.name}/${playbook.filename}`;
        onProgress(progress());
        const saved = await client.savePlaybook(release.id, playbook.filename, playbook.content);
        managedPaths.set(playbook.filename, saved.path);
      }
      stage = `绑定 Action：${entry.component.name} ${entry.release.version ?? ''}`;
      onProgress(progress());
      release = await client.updateRelease(release.id, {
        ...entry.release,
        id: release.id,
        componentId: component.id,
        state: 'draft',
        dependencies,
        actions: desiredActions.map((action) => ({ ...action, playbook: managedPaths.get(action.playbook)! })),
      });
      releases.set(entry.component.slug, release);
      completedReleases.push(entry.component.slug);
    }
    return { components, releases };
  } catch (reason) {
    const message = reason instanceof Error ? reason.message : '未知错误';
    throw new ComponentImportExecutionError(`${stage}失败：${message}`, progress());
  }
}
