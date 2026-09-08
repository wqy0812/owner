import { beforeEach, vi } from 'vitest';
import type { EnvironmentParameterField } from '../../types/domain';
import { unexpectedRequest } from '../testLifecycle';

export const alice = { id: 'component-owner-a', name: '林晓', role: 'component_owner' };
export const bob = { id: 'component-owner-b', name: '周工', role: 'component_owner' };
export const dave = { id: 'environment-owner-a', name: '王维', role: 'environment_owner' };
export const carol = { id: 'scenario-owner-a', name: '陈晨', role: 'scenario_owner' };
export const admin = { id: 'platform-admin', name: '赵宁', role: 'platform_admin' };
export const optionUsage = { componentReleases: 0, scenarioRevisions: 0, environmentRevisions: 0 };
const categoryTemplates = [
  { id: 'architecture', key: 'architecture', label: '架构', kind: 'environment_dimension', environmentRequired: true, sortOrder: 1, createdBy: 'seed', createdAt: '', usage: optionUsage, options: [{ id: 'amd64', categoryId: 'architecture', value: 'amd64', label: 'x86/amd64', sortOrder: 1, createdBy: 'seed', createdAt: '', usage: optionUsage }, { id: 'arm64', categoryId: 'architecture', value: 'arm64', label: 'ARM/arm64', sortOrder: 2, createdBy: 'seed', createdAt: '', usage: optionUsage }] },
  { id: 'operatingSystem', key: 'operatingSystem', label: '操作系统', kind: 'environment_dimension', environmentRequired: true, sortOrder: 2, createdBy: 'seed', createdAt: '', usage: optionUsage, options: ['Ubuntu', 'SUSE', 'Kylin'].map((value, index) => ({ id: `os-${value}`, categoryId: 'operatingSystem', value, label: value, sortOrder: index, createdBy: 'seed', createdAt: '', usage: optionUsage })) },
  { id: 'operatingSystemVersion', key: 'operatingSystemVersion', label: '操作系统版本', kind: 'environment_dimension', environmentRequired: true, sortOrder: 3, createdBy: 'seed', createdAt: '', usage: optionUsage, options: ['18.04', '20.04', '22.04', '24.04'].map((value, index) => ({ id: `osv-${value}`, categoryId: 'operatingSystemVersion', value, label: value, sortOrder: index, createdBy: 'seed', createdAt: '', usage: optionUsage })) },
  { id: 'containerRuntime', key: 'containerRuntime', label: '容器运行时', kind: 'environment_dimension', environmentRequired: true, sortOrder: 4, createdBy: 'seed', createdAt: '', usage: optionUsage, options: [{ id: 'runtime-docker', categoryId: 'containerRuntime', value: 'docker', label: 'Docker', sortOrder: 1, createdBy: 'seed', createdAt: '', usage: optionUsage }, { id: 'runtime-containerd', categoryId: 'containerRuntime', value: 'containerd', label: 'containerd', sortOrder: 2, createdBy: 'seed', createdAt: '', usage: optionUsage }] },
  { id: 'containerRuntimeVersion', parentCategoryId: 'containerRuntime', key: 'containerRuntimeVersion', label: '运行时版本', kind: 'environment_dimension', environmentRequired: true, sortOrder: 5, createdBy: 'seed', createdAt: '', usage: optionUsage, options: [{ id: 'runtime-docker-20.10.21', categoryId: 'containerRuntimeVersion', parentOptionId: 'runtime-docker', value: 'docker@20.10.21', label: '20.10.21', sortOrder: 1, createdBy: 'seed', createdAt: '', usage: optionUsage }, { id: 'runtime-docker-20.10.24', categoryId: 'containerRuntimeVersion', parentOptionId: 'runtime-docker', value: 'docker@20.10.24', label: '20.10.24', sortOrder: 2, createdBy: 'seed', createdAt: '', usage: optionUsage }, { id: 'runtime-containerd-2.0.10', categoryId: 'containerRuntimeVersion', parentOptionId: 'runtime-containerd', value: 'containerd@2.0.10', label: '2.0.10', sortOrder: 3, createdBy: 'seed', createdAt: '', usage: optionUsage }] },
  { id: 'ipFamily', key: 'ipFamily', label: 'IP 协议族', kind: 'environment_dimension', environmentRequired: true, sortOrder: 5, createdBy: 'seed', createdAt: '', usage: optionUsage, options: ['IPv4', 'IPv6'].map((value, index) => ({ id: `ip-${value}`, categoryId: 'ipFamily', value, label: value, sortOrder: index, createdBy: 'seed', createdAt: '', usage: optionUsage })) },
  { id: 'hostGroup', key: 'hostGroup', label: '主机组', kind: 'host_group', environmentRequired: false, sortOrder: 100, createdBy: 'seed', createdAt: '', usage: optionUsage, options: ['all', 'bootstrap_host', 'runtime_nodes', 'control_plane'].map((value, index) => ({ id: `group-${value}`, categoryId: 'hostGroup', value, label: value, sortOrder: index, createdBy: 'seed', createdAt: '', usage: optionUsage })) },
];

const componentTemplate = [{
  id: 'component-containerd',
  name: 'containerd',
  slug: 'containerd',
  ownerId: alice.id,
  description: 'CRI runtime',
  layer: 'runtime_state',
  tags: ['runtime'],
  latestRelease: { id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released', readiness: { status: 'ready', blockers: [] }, actions: [{ kind: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { kind: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { kind: 'rollback', playbook: 'rollback.yml' }] },
  releases: [{ id: 'release-containerd-2', componentId: 'component-containerd', version: 'v2.1.1', status: 'released', readiness: { status: 'ready', blockers: [] }, actions: [{ kind: 'upgrade', playbook: 'upgrade.yml', requiredCredentials: ['ansible_ssh_pass', 'registry_user'] }, { kind: 'verify', playbook: 'verify.yml', requiredCredentials: ['ansible_ssh_pass'] }, { kind: 'rollback', playbook: 'rollback.yml' }] }],
}];

// Every test owns its catalog data, including nested arrays changed by a response fixture.
export let components = structuredClone(componentTemplate);
export let platformOptionCategories = structuredClone(categoryTemplates);
beforeEach(() => {
  components = structuredClone(componentTemplate);
  platformOptionCategories = structuredClone(categoryTemplates);
});

export function json(data: unknown, status = 200) {
  const completeReleaseDTOs = (value: unknown): unknown => {
    if (Array.isArray(value)) return value.map(completeReleaseDTOs);
    if (!value || typeof value !== 'object') return value;
    const result = Object.fromEntries(Object.entries(value).map(([key, item]) => [key, completeReleaseDTOs(item)]));
    if (typeof result.kind === 'string' && typeof result.environmentId === 'string') {
      result.name ??= result.componentName ?? result.scenarioName ?? result.id;
      result.environmentName ??= 'Test Environment'; result.createdAt ??= '';
    }
    if (typeof result.componentId === 'string' && typeof result.version === 'string' && typeof result.status === 'string') {
      result.lineId ??= `line-${result.componentId}`;
      result.lineName ??= `${result.componentId} baseline`;
      result.compatibility ??= result.parentReleaseId ? 'compatible' : 'not_applicable';
      result.readiness ??= { status: 'ready', blockers: [] };
      result.review ??= { status: 'approved' };
      result.dependencies ??= [];
      const mappedTargets = new Set((result.dependencies as any[]).flatMap((dependency) => (dependency.parameterMappings ?? []).map((mapping: any) => mapping.targetParameter)));
      result.parameters = Array.isArray(result.parameters) ? result.parameters.map((parameter: any) => {
        if (parameter.valueProvider) return parameter;
        if (mappedTargets.has(parameter.name)) return { modifiable: false, valueProvider: 'upstream_mapping', ...parameter };
        const fixedValue = parameter.type === 'boolean' ? false : parameter.type === 'integer' || parameter.type === 'number' ? 0 : parameter.type === 'array' ? [] : parameter.type === 'object' ? {} : '/opt/test';
        return { modifiable: false, valueProvider: 'component_owner', fixedValue, ...parameter };
      }) : [];
      result.actions = Array.isArray(result.actions) ? result.actions.map((action: any) => ({ id: `action-${action.kind}`, hostGroup: 'all', ...action })) : [];
      result.artifacts ??= [];
      result.images ??= [];
    }
    if (typeof result.ownerId === 'string' && Array.isArray(result.releases) && !result.releaseLines) {
      const grouped = new Map<string, any[]>();
      for (const release of result.releases as any[]) {
        const values = grouped.get(release.lineId) ?? [];
        values.push(release);
        grouped.set(release.lineId, values);
      }
      result.releaseLines = [...grouped.entries()].map(([id, releases]) => ({
        id, componentId: result.id, name: releases[0]?.lineName ?? id, createdAt: '2026-08-01T00:00:00Z', releaseIds: releases.map((release) => release.id),
        latestReleasedId: releases.find((release) => release.status === 'released')?.id,
        currentDraftId: releases.find((release) => release.status === 'draft')?.id,
        evolutionEligible: !releases.some((release) => release.status === 'draft') && releases[0]?.status === 'released',
        evolutionParentId: !releases.some((release) => release.status === 'draft') && releases[0]?.status === 'released' ? releases[0].id : undefined,
      }));
    }
    if (typeof result.ownerId === 'string' && Array.isArray(result.releases)) {
      result.ownerName ??= '林晓'; result.releaseCount ??= result.releases.length;
      result.hasDraft ??= result.releases.some((release: any) => release.status === 'draft');
      result.needsAttention ??= result.hasDraft || !result.releases.length || result.releases[0]?.readiness?.status === 'blocked';
      if (Array.isArray(result.releaseLines)) result.releaseLines = result.releaseLines.map((line: any) => {
        if (!line.releases) return line;
        const { releases, ...metadata } = line;
        return { ...metadata, releaseIds: releases.map((release: any) => release.id) };
      });
    }
    return result;
  };
  const currentContractData = completeReleaseDTOs(data);
  const body = status >= 400 ? data : Array.isArray(data) ? { items: currentContractData } : { data: currentContractData };
  return Promise.resolve(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }));
}

export function isEnvironmentList(url: string) {
  return url.endsWith('/environments') || url.endsWith('/environments?includeArchived=true');
}

export function installFetch(options: {
  componentCreateForbidden?: boolean;
  initialUser?: typeof alice | typeof dave | typeof carol | typeof admin;
  notifications?: Array<Record<string, unknown>>;
  withScenario?: boolean;
  parameterDefinitions?: Array<Record<string, unknown>>;
  parameterFields?: EnvironmentParameterField[];
  environmentParameters?: Record<string, unknown>;
} = {}) {
  let current = options.initialUser ?? alice;
  let environmentRevision = 1;
  let environmentParameters = structuredClone(options.environmentParameters ?? {});
  const environment = () => ({ id: 'environment-test', name: 'Test Environment', ownerId: dave.id, currentRevision: { id: `environment-test-r${environmentRevision}`, environmentId: 'environment-test', revision: environmentRevision, facts: {}, hosts: [], parameters: environmentParameters, variables: {}, credentialRefs: [] } });
  let notifications = structuredClone(options.notifications ?? []);
  let parameterDefinitions = structuredClone(options.parameterDefinitions ?? []);
  const mock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const parsedURL = new URL(url, 'http://test');
    const method = init?.method ?? 'GET';
    const writes: Record<string, RegExp[]> = {
      POST: [/^\/api\/v1\/(session\/switch|components|platform-option-categories|environment-variable-definitions)$/, /^\/api\/v1\/platform-option-categories\/[^/]+\/options$/, /^\/api\/v1\/catalog-repository\/(restore-plan|restore|backups|create|connect)$/],
      PUT: [/^\/api\/v1\/environment-parameter-definitions\/[^/]+\/default$/, /^\/api\/v1\/environments\/environment-test\/parameters$/],
      PATCH: [/^\/api\/v1\/notifications\/[^/]+$/],
    };
    if (method !== 'GET' && !writes[method]?.some(pattern => pattern.test(parsedURL.pathname))) return unexpectedRequest(input, init);
    if (parsedURL.pathname === '/api/v1/components' && parsedURL.searchParams.get('view') === 'contracts') return json(components);
    if (/^\/api\/v1\/components\/[^/]+\/usage$/.test(parsedURL.pathname)) return json({ components: [], scenarios: [], componentCount: 0, scenarioCount: 0 });
    if (/^\/api\/v1\/component-releases\/[^/]+\/playbook-workspace$/.test(parsedURL.pathname) && (!init?.method || init.method === 'GET')) return json({ root: 'managed/test/', treeSha256: 'empty-workspace-tree', files: [] });
    if (parsedURL.pathname === '/api/v1/runs' && parsedURL.search) return new Response(JSON.stringify({ items: [], total: 0, page: Number(parsedURL.searchParams.get('page') ?? 1), pageSize: Number(parsedURL.searchParams.get('pageSize') ?? 50) }), { headers: { 'Content-Type': 'application/json' } });
    if (/^\/api\/v1\/scenario-revisions\/[^/]+\/acceptance$/.test(parsedURL.pathname)) return json({ revisionId: parsedURL.pathname.split('/').at(-2), revisionDigest: 'acceptance-digest', editable: true, jobs: [], parameters: [], values: {}, bindings: [], workspace: { root: 'managed/scenario/', treeSha256: 'acceptance-tree', files: [] } });
    if (parsedURL.pathname === '/api/v1/runs/run-self/activity') return json({ runId: 'run-self', status: 'failed', nextAfterId: 0, logs: [], waitingObservations: [], hasMore: false, archived: false });
    if (parsedURL.pathname === '/api/v1/runs/run-self/diagnostics') return json({ runId: 'run-self', status: 'failed', capturedAt: '', lastLogId: 0, logCount: 0, items: [] });
    if (url.endsWith('/session/users')) return json([alice, bob, dave, admin, carol]);
    if (url.endsWith('/session/me')) return json(current);
    if (url.endsWith('/platform-option-categories') && (!init?.method || init.method === 'GET')) return json(platformOptionCategories);
    if (url.endsWith('/session/switch')) {
      const id = JSON.parse(String(init?.body)).userId;
      current = id === dave.id ? dave : id === carol.id ? carol : id === admin.id ? admin : alice;
      return json(current);
    }
    if (url.endsWith('/workbench')) return json({
      generatedAt: '2026-08-25T10:00:00Z', role: current.role,
      summary: { critical: 0, actionRequired: 0, inProgress: 0, informational: 0 },
      assets: { components: current.role === 'component_owner' ? 1 : 0, scenarios: current.role === 'scenario_owner' ? 1 : 0, environments: current.role === 'environment_owner' ? 1 : 0 },
      items: [],
    });
    if (url.endsWith('/components') && init?.method === 'POST') {
      if (options.componentCreateForbidden) return json({ error: { code: 'FORBIDDEN', message: '只有资源 Owner 可以修改组件' } }, 403);
      return json(components[0]);
    }
    if (url.endsWith('/components')) return json(components);
    if (url.endsWith('/scenarios')) return json(options.withScenario ? [{
      id: 'scenario-sample',
      name: 'Sample Cluster Build',
      ownerId: carol.id,
      slug: 'sample-cluster-build',
      currentRevisionId: 'scenario-sample-r1',
      currentRevision: {
        id: 'scenario-sample-r1',
        scenarioId: 'scenario-sample',
        revision: 1,
        state: 'draft',
        nodes: [{ id: 'component-step', type: 'component', position: { x: 80, y: 80 }, data: { label: 'component-step', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', parameterValues: {} } }],
        edges: [],
      },
      revisions: [{
        id: 'scenario-sample-r1',
        scenarioId: 'scenario-sample',
        revision: 1,
        state: 'draft',
        nodes: [{ id: 'component-step', type: 'component', position: { x: 80, y: 80 }, data: { label: 'component-step', componentId: 'component-containerd', releaseId: 'release-containerd-2', action: 'rollback', parameterValues: {} } }],
        edges: [],
      }],
    }] : []);
    if (url.endsWith('/catalog-repository/restore-plan')) return json({ gitCommit: 'a'.repeat(40), schemaContract: 'clusterforge-v1', catalogSha256: 'b'.repeat(64), counts: { components: 2, component_releases: 3, scenarios: 1 }, playbookCount: 4, targetComponentCount: 0, targetScenarioCount: 0, planDigest: 'c'.repeat(64) });
    if (url.endsWith('/catalog-repository/restore')) return json({ restored: true, gitCommit: 'a'.repeat(40), schemaContract: 'clusterforge-v1', catalogSha256: 'b'.repeat(64), counts: { components: 2, component_releases: 3, scenarios: 1 }, playbookCount: 4, targetComponentCount: 0, targetScenarioCount: 0, planDigest: 'c'.repeat(64) }, 201);
    if (url.endsWith('/catalog-repository/backups')) return json({ backupId: '20260829T080000Z-test', status: 'success', reason: 'manual-ui', createdAt: '2026-08-29T08:00:00Z', completedAt: '2026-08-29T08:00:01Z', publicationGeneration: 7, gitCommit: 'd'.repeat(40), gitTag: 'backup/20260829T080000Z-test' }, 201);
    if (url.endsWith('/catalog-repository/create') || url.endsWith('/catalog-repository/connect')) return json({ enabled: true, configured: true, path: '/data/private/catalog.git', branch: 'catalog', allowedRoot: '/data/private', recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }], restoreTargetKnown: true, targetCatalogEmpty: true, targetComponentCount: 0, targetScenarioCount: 0 }, url.endsWith('/catalog-repository/create') ? 201 : 200);
    if (url.endsWith('/catalog-repository')) return json({ enabled: true, configured: true, path: '/data/private/catalog.git', branch: 'catalog', allowedRoot: '/data/private', recoveryPoints: [{ ref: 'backup/20260829', commit: 'a'.repeat(40), createdAt: '2026-08-29T08:00:00Z' }], restoreTargetKnown: true, targetCatalogEmpty: true, targetComponentCount: 0, targetScenarioCount: 0 });
    if (url.endsWith('/environment-parameter-definitions')) return json(parameterDefinitions);
    if (/\/environment-parameter-definitions\/[^/]+\/default$/.test(url) && init?.method === 'PUT') {
      const id = url.split('/').at(-2);
      const { defaultValue } = JSON.parse(String(init.body));
      parameterDefinitions = parameterDefinitions.map((definition) => definition.id === id ? { ...definition, defaultValue: defaultValue ?? undefined } : definition);
      return json(parameterDefinitions.find((definition) => definition.id === id));
    }
    if (url.endsWith('/environment-parameter-fields')) return json(options.parameterFields ?? []);
    if (url.endsWith('/environment-variable-definitions')) return init?.method === 'POST' ? json({ id: 'variable-new', ...JSON.parse(String(init.body)) }) : json([]);
    if (isEnvironmentList(url)) return json([environment()]);
    if (url.endsWith('/environments/environment-test/parameters') && init?.method === 'PUT') {
      environmentParameters = JSON.parse(String(init.body)).values;
      environmentRevision += 1;
      return json(environment());
    }
    if (url.endsWith('/run-retention')) return json({ configured: false, pending: 0, sizeBytes: 0, tasks: [], policy: { autoArchive: false, autoCleanup: false, archiveDays: 90, cleanupDays: 90 } });
    if (url.endsWith('/runs')) return json([]);
    if (/\/notifications\/[^/]+$/.test(url) && init?.method === 'PATCH') {
      const id = decodeURIComponent(url.split('/').pop() ?? '');
      notifications = notifications.map((item) => item.id === id ? { ...item, read: true } : item);
      return json(notifications.find((item) => item.id === id));
    }
    if (url.endsWith('/notifications')) return json(notifications);
    if (/\/environments\/[^/]+\/credential-sources(?:\?.*)?$/.test(url)) return json([]);
    if (url.endsWith('/platform-option-categories') && init?.method === 'POST') return json({ id: 'category-new', ...JSON.parse(String(init.body)) });
    if (/\/platform-option-categories\/[^/]+\/options$/.test(url) && init?.method === 'POST') return json({ id: 'option-new', ...JSON.parse(String(init.body)) });
    return unexpectedRequest(input, init);
  });
  fallbackFetch = mock;
  vi.stubGlobal('fetch', mock);
  return mock;
}

// In-memory catalog handlers expose the three current read models separately.
// Scheduling tests provide their own explicit handlers and bypass this fixture.
export function installReadModelFixtures() {
  const currentFetch = globalThis.fetch;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://test');
    if (url.pathname === '/api/v1/execution-preparations' && init?.method === 'POST') {
      const request = JSON.parse(String(init.body));
      const endpoint = request.kind === 'component_test' ? `/api/v1/component-releases/${request.subjectId}/test-plan` : `/api/v1/scenario-revisions/${request.subjectId}/execution-plan`;
      const response = await currentFetch(endpoint, { ...init, body: JSON.stringify(request.kind === 'component_test' ? request.component : request.scenario) });
      const result = await response.json();
      return json({ id: `preparation-${request.idempotencyKey}`, status: response.ok ? 'succeeded' : 'failed', input: request, output: { checks: [], ...(response.ok ? { plan: result.data } : { error: result.error.message, explanation: result.error.explanation }) } });
    }
    if ((!init?.method || init.method === 'GET') && /^\/api\/v1\/components\/[^/]+\/usage$/.test(url.pathname)) return json({ components: [], scenarios: [], componentCount: 0, scenarioCount: 0 });
    if ((!init?.method || init.method === 'GET') && url.pathname === '/api/v1/run-retention') return json({ configured: false, policy: { autoArchive: false, autoCleanup: false, archiveDays: 90, cleanupDays: 90 }, pending: 0, sizeBytes: 0, tasks: [] });
    if (url.pathname.endsWith('/platform-option-categories') && (!init?.method || init.method === 'GET')) return json(platformOptionCategories);
    if ((!init?.method || init.method === 'GET') && url.pathname === '/api/v1/components' && !url.search) {
      const response = await currentFetch('/api/v1/components', init);
      if (!response.ok) return response;
      const body = await response.json();
      return json((body.items ?? []).map((component: Record<string, unknown>) => Object.fromEntries([
        'id', 'name', 'slug', 'description', 'ownerId', 'ownerName', 'layer', 'tags',
        'releaseCount', 'defaultReleaseId', 'hasDraft', 'needsAttention',
      ].map(key => [key, component[key]]))));
    }
    if ((!init?.method || init.method === 'GET') && url.pathname === '/api/v1/components' && url.searchParams.get('view') === 'contracts') return currentFetch('/api/v1/components', init);
    if ((!init?.method || init.method === 'GET') && /^\/api\/v1\/components\/[^/]+$/.test(url.pathname)) {
      const response = await currentFetch('/api/v1/components', init);
      if (!response.ok) return response;
      const body = await response.json(); const component = body.items?.find((item: any) => item.id === url.pathname.split('/').pop());
      if (!component) return json({ error: { code: 'not_found', message: '组件不存在' } }, 404);
      const workResponse = await currentFetch('/api/v1/workbench', init); const work = await workResponse.json();
      const readContext = component.readContext ?? { evidence: {}, workItems: (work.data?.items ?? []).filter((item: any) => item.subject.type === 'component_release' && component.releases.some((release: any) => release.id === item.subject.id)), parameterConsumers: [] };
      return json({ ...component, readContext });
    }
    if ((!init?.method || init.method === 'GET') && url.pathname === '/api/v1/runs' && url.search) {
      const response = await currentFetch('/api/v1/runs', init); if (!response.ok) return response;
      const body = await response.json(); const page = Number(url.searchParams.get('page')); const pageSize = Number(url.searchParams.get('pageSize'));
      const active = new Set(['running', 'queued', 'awaiting_approval']);
      const items = (body.items ?? []).filter((run: any) => (!url.searchParams.get('environmentId') || run.environmentId === url.searchParams.get('environmentId')) && (url.searchParams.get('filter') === 'active' ? active.has(run.status) : url.searchParams.get('filter') === 'finished' ? !active.has(run.status) : true));
      return new Response(JSON.stringify({ items: items.slice((page - 1) * pageSize, page * pageSize).map((run: any) => ({ ...run, name: run.name ?? run.componentName ?? run.scenarioName ?? run.id, environmentName: run.environmentName ?? 'Test Environment', createdAt: run.createdAt ?? '' })), page, pageSize, total: items.length }), { headers: { 'Content-Type': 'application/json' } });
    }
    if ((!init?.method || init.method === 'GET') && url.pathname === '/api/v1/approvals/batch-candidates') {
      const response = await currentFetch('/api/v1/runs', init); const body = await response.json();
      return json((body.items ?? []).filter((run: any) => run.status === 'awaiting_approval' && run.approval?.id && !run.deliveryRequirements?.length).map((run: any) => ({ ...run, name: run.name ?? run.id, environmentName: run.environmentName ?? 'Test Environment', createdAt: run.createdAt ?? '', approvalId: run.approval.id })));
    }
    return currentFetch(input, init);
  }));
}


let fallbackFetch: ReturnType<typeof installFetch>;
/** Unspecified reads use the explicit baseline routes; every unknown route fails the test. */
export function defaultResponse(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  return fallbackFetch(input, init);
}
