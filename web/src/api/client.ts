import type {
  ActionDefinition,
  Approval,
  Component,
  ComponentDependency,
  ComponentRelease,
  CredentialRef,
  Environment,
  EnvironmentHost,
  EnvironmentRevision,
  ImpactPreview,
  Notification,
  Run,
  RunStep,
  Scenario,
  ScenarioEdge,
  ScenarioNode,
  ScenarioRevision,
  User,
} from '../types/domain';

const API_ROOT = '/api/v1';

interface ApiErrorBody {
  error?: {
    code?: string;
    message?: string;
    details?: unknown;
  };
  message?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;

  constructor(status: number, body: ApiErrorBody) {
    super(body.error?.message ?? body.message ?? `请求失败（HTTP ${status}）`);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.error?.code ?? `HTTP_${status}`;
    this.details = body.error?.details;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  headers.set('Accept', 'application/json');

  let response: Response;
  try {
    response = await fetch(`${API_ROOT}${path}`, {
      ...init,
      headers,
      credentials: 'include',
    });
  } catch (reason) {
    if (isAbortError(reason)) throw reason;
    throw new ApiError(0, {
      error: { code: 'NETWORK_ERROR', message: '无法连接平台 API，请确认 Go 服务已启动。' },
    });
  }

  const text = await response.text();
  const payload = text ? parseJSON(text) : undefined;
  if (!response.ok) throw new ApiError(response.status, isApiErrorBody(payload) ? payload : {});
  if (!response.headers.get('Content-Type')?.toLowerCase().includes('application/json')) {
    throw invalidResponse(response.status, '平台 API 返回了非 JSON 响应。');
  }
  if (text && payload === undefined) throw invalidResponse(response.status, '平台 API 返回了损坏的 JSON 响应。');
  return payload as T;
}

function parseJSON(value: string): unknown | undefined {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

function isApiErrorBody(value: unknown): value is ApiErrorBody {
  return typeof value === 'object' && value !== null;
}

function isAbortError(reason: unknown): boolean {
  return (reason instanceof DOMException || reason instanceof Error) && reason.name === 'AbortError';
}

function invalidResponse(status: number, message: string): ApiError {
  return new ApiError(status, { error: { code: 'INVALID_RESPONSE', message } });
}

function unwrapList(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  const body = record(value);
  const items = body && (body.items ?? body.data);
  if (Array.isArray(items)) return items;
  throw invalidResponse(200, '平台 API 返回了无效的列表响应。');
}

function unwrap(value: unknown): unknown {
  const body = record(value);
  return body && 'data' in body ? body.data : value;
}

type LooseRecord = Record<string, unknown>;

const RELEASE_STATES = ['draft', 'released', 'deprecated'] as const;
const COMPONENT_LAYERS = ['host_foundation', 'runtime_state', 'orchestration_core', 'cluster_service', 'observability_management', 'platform_extension'] as const;
const COMPONENT_CATEGORIES = ['preflight', 'bootstrap', 'security', 'runtime', 'state_store', 'control_plane', 'worker', 'network', 'dns', 'ingress', 'storage', 'observability', 'node_management', 'platform', 'autoscaling'] as const;
const COMPONENT_KINDS = ['software', 'software_bundle', 'delivery_stage', 'configuration', 'artifact_set'] as const;
const COMPONENT_REQUIREDNESS = ['core_required', 'profile_required', 'optional'] as const;
const ACTION_TYPES = ['inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall'] as const;
const RUN_STATUSES = ['queued', 'awaiting_approval', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted', 'rejected'] as const;
const STEP_STATUSES = [...RUN_STATUSES, 'pending', 'skipped'] as const;
const CREDENTIAL_TYPES = ['sshKeyPath', 'envVarRef'] as const;

function isRecord(value: unknown): value is LooseRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function record(value: unknown): LooseRecord | undefined {
  return isRecord(value) ? value : undefined;
}

function requireRecord(value: unknown, field: string): LooseRecord {
  if (isRecord(value)) return value;
  throw invalidResponse(200, `平台 API 缺少或错误返回了 ${field}。`);
}

function field(raw: LooseRecord, ...keys: string[]): unknown {
  for (const key of keys) {
    const value = raw[key];
    if (value !== undefined && value !== null) return value;
  }
  return undefined;
}

function optionalString(raw: LooseRecord, ...keys: string[]): string | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  if (typeof value === 'string') return value;
  throw invalidResponse(200, `平台 API 的 ${keys[0]} 必须是字符串。`);
}

function requireString(raw: LooseRecord, ...keys: string[]): string {
  const value = optionalString(raw, ...keys);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${keys[0]}。`);
}

function optionalNumber(raw: LooseRecord, ...keys: string[]): number | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  throw invalidResponse(200, `平台 API 的 ${keys[0]} 必须是数字。`);
}

function requireNumber(raw: LooseRecord, ...keys: string[]): number {
  const value = optionalNumber(raw, ...keys);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${keys[0]}。`);
}

function optionalBoolean(raw: LooseRecord, ...keys: string[]): boolean | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  if (typeof value === 'boolean') return value;
  throw invalidResponse(200, `平台 API 的 ${keys[0]} 必须是布尔值。`);
}

function requireBoolean(raw: LooseRecord, ...keys: string[]): boolean {
  const value = optionalBoolean(raw, ...keys);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${keys[0]}。`);
}

function optionalRecord(raw: LooseRecord, ...keys: string[]): LooseRecord | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  return requireRecord(value, keys[0]);
}

function requireRecords(raw: LooseRecord, fieldName: string): LooseRecord[] {
  const value = raw[fieldName];
  if (!Array.isArray(value)) throw invalidResponse(200, `平台 API 的 ${fieldName} 必须是数组。`);
  return value.map((item, index) => requireRecord(item, `${fieldName}[${index}]`));
}

function optionalRecords(raw: LooseRecord, ...keys: string[]): LooseRecord[] | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw invalidResponse(200, `平台 API 的 ${keys[0]} 必须是数组。`);
  return value.map((item, index) => requireRecord(item, `${keys[0]}[${index}]`));
}

function optionalStringArray(raw: LooseRecord, ...keys: string[]): string[] | undefined {
  const value = field(raw, ...keys);
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || !value.every((item) => typeof item === 'string')) {
    throw invalidResponse(200, `平台 API 的 ${keys[0]} 必须是字符串数组。`);
  }
  return value;
}

function requireStringArray(raw: LooseRecord, ...keys: string[]): string[] {
  const value = optionalStringArray(raw, ...keys);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${keys[0]}。`);
}

function optionalEnum<T extends string>(raw: LooseRecord, values: readonly T[], ...keys: string[]): T | undefined {
  const value = optionalString(raw, ...keys);
  if (value === undefined) return undefined;
  if ((values as readonly string[]).includes(value)) return value as T;
  throw invalidResponse(200, `平台 API 的 ${keys[0]} 值 ${JSON.stringify(value)} 不受支持。`);
}

function requireEnum<T extends string>(raw: LooseRecord, values: readonly T[], ...keys: string[]): T {
  const value = optionalEnum(raw, values, ...keys);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${keys[0]}。`);
}

function optionalObject(raw: LooseRecord, ...keys: string[]): Record<string, unknown> | undefined {
  return optionalRecord(raw, ...keys);
}

function normalizeAction(raw: LooseRecord): ActionDefinition {
  const risk = optionalEnum(raw, ['normal', 'destructive'] as const, 'risk');
  return {
    id: optionalString(raw, 'id'),
    type: requireEnum(raw, ACTION_TYPES, 'type', 'kind'),
    playbook: requireString(raw, 'playbook'),
    tags: optionalStringArray(raw, 'tags'),
    hostGroup: optionalString(raw, 'hostGroup', 'host_group'),
    timeoutSeconds: optionalNumber(raw, 'timeoutSeconds', 'timeout_seconds'),
    allowedParameters: optionalStringArray(raw, 'allowedParameters', 'allowed_parameters'),
    requiredCredentials: optionalStringArray(raw, 'requiredCredentials', 'required_credentials'),
    risk: risk ?? (optionalBoolean(raw, 'destructive') ? 'destructive' : undefined),
    fromReleaseId: optionalString(raw, 'fromReleaseId', 'from_release_id'),
    toReleaseId: optionalString(raw, 'toReleaseId', 'to_release_id'),
  };
}

function normalizeDependency(raw: LooseRecord): ComponentDependency {
  return {
    componentId: requireString(raw, 'componentId', 'component_id', 'upstreamComponentId'),
    componentName: optionalString(raw, 'componentName', 'upstreamComponentName'),
    releaseId: requireString(raw, 'upstreamReleaseId', 'upstream_release_id', 'releaseId', 'release_id'),
    version: optionalString(raw, 'version'),
    purpose: optionalString(raw, 'purpose'),
  };
}

function normalizeRelease(raw: LooseRecord, componentID?: string): ComponentRelease {
  const state = requireEnum(raw, RELEASE_STATES, 'state', 'status');
  const resolvedComponentID = optionalString(raw, 'componentId', 'component_id') ?? componentID;
  if (!resolvedComponentID) throw invalidResponse(200, '平台 API 缺少必填字段 componentId。');
  const actions = optionalRecords(raw, 'actions')?.map(normalizeAction);
  const dependencies = optionalRecords(raw, 'dependencies')?.map(normalizeDependency);
  return {
    id: requireString(raw, 'id'),
    componentId: resolvedComponentID,
    version: requireString(raw, 'version'),
    type: optionalEnum(raw, ['atomic', 'bundle'] as const, 'type'),
    state,
    status: state,
    verification: optionalEnum(raw, ['unverified', 'testing', 'passed', 'failed'] as const, 'verification'),
    verified: optionalBoolean(raw, 'verified'),
    breaking: optionalBoolean(raw, 'breaking'),
    releaseNotes: optionalString(raw, 'releaseNotes', 'release_notes'),
    dependencies,
    environmentConstraints: optionalObject(raw, 'environmentConstraints', 'environment_constraints'),
    parameterSchema: optionalObject(raw, 'parameterSchema', 'parameter_schema'),
    actions,
    createdAt: optionalString(raw, 'createdAt', 'created_at'),
    releasedAt: optionalString(raw, 'releasedAt', 'released_at'),
  };
}

function normalizeReleaseActionResponse(value: unknown, componentID?: string): ComponentRelease {
  const raw = requireRecord(normalizeOptionalData(value), 'release');
  return normalizeRelease(raw, componentID);
}

function normalizeComponent(raw: LooseRecord): Component {
  const releases = optionalRecords(raw, 'releases')?.map((release) => normalizeRelease(release));
  const latest = optionalRecord(raw, 'latestRelease', 'latest_release');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    slug: optionalString(raw, 'slug'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId', 'owner_id'),
    ownerName: optionalString(raw, 'ownerName', 'owner_name'),
    layer: requireEnum(raw, COMPONENT_LAYERS, 'layer'),
    category: requireEnum(raw, COMPONENT_CATEGORIES, 'category'),
    kind: requireEnum(raw, COMPONENT_KINDS, 'kind'),
    requiredness: requireEnum(raw, COMPONENT_REQUIREDNESS, 'requiredness'),
    latestRelease: latest ? normalizeRelease(latest) : releases?.[0],
    releases,
    releaseCount: optionalNumber(raw, 'releaseCount', 'release_count'),
    updatedAt: optionalString(raw, 'updatedAt', 'updated_at'),
  };
}

function normalizePosition(raw: LooseRecord): { x: number; y: number } {
  return { x: requireNumber(raw, 'x'), y: requireNumber(raw, 'y') };
}

function normalizeBindings(raw: LooseRecord): Record<string, string> {
  const bindings: Record<string, string> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value !== 'string') throw invalidResponse(200, `平台 API 的 bindings.${key} 必须是字符串。`);
    bindings[key] = value;
  }
  return bindings;
}

function normalizeScenarioNode(raw: LooseRecord): ScenarioNode {
  const data = requireRecord(raw.data, 'node.data');
  return {
    id: requireString(raw, 'id'),
    type: optionalString(raw, 'type') ?? 'component',
    position: normalizePosition(requireRecord(raw.position, 'node.position')),
    data: {
      label: requireString(data, 'label'),
      componentId: requireString(data, 'componentId'),
      releaseId: requireString(data, 'releaseId'),
      version: optionalString(data, 'version'),
      action: optionalEnum(data, ACTION_TYPES, 'action'),
      hostGroup: optionalString(data, 'hostGroup'),
      values: optionalObject(data, 'values'),
      bindings: optionalRecord(data, 'bindings') ? normalizeBindings(optionalRecord(data, 'bindings')!) : undefined,
      runInputs: optionalStringArray(data, 'runInputs'),
      layer: optionalEnum(data, COMPONENT_LAYERS, 'layer'),
    },
  };
}

function normalizeScenarioEdge(raw: LooseRecord): ScenarioEdge {
  return {
    id: requireString(raw, 'id'),
    source: requireString(raw, 'source'),
    target: requireString(raw, 'target'),
    label: optionalString(raw, 'label'),
  };
}

function normalizeRevision(raw: LooseRecord): ScenarioRevision {
  return {
    id: requireString(raw, 'id'),
    scenarioId: requireString(raw, 'scenarioId', 'scenario_id'),
    revision: requireNumber(raw, 'revision'),
    state: requireEnum(raw, ['draft', 'testing', 'test_passed', 'released', 'deprecated'] as const, 'state', 'status'),
    nodes: requireRecords(raw, 'nodes').map(normalizeScenarioNode),
    edges: requireRecords(raw, 'edges').map(normalizeScenarioEdge),
    runInputs: optionalStringArray(raw, 'runInputs', 'run_inputs'),
    executionPolicy: optionalObject(raw, 'executionPolicy', 'execution_policy'),
    validationErrors: optionalStringArray(raw, 'validationErrors'),
    testedAt: optionalString(raw, 'testedAt', 'testPassedAt'),
    createdAt: optionalString(raw, 'createdAt'),
  };
}

function normalizeScenario(raw: LooseRecord): Scenario {
  const revisions = optionalRecords(raw, 'revisions')?.map(normalizeRevision);
  const current = optionalRecord(raw, 'currentRevision', 'current_revision', 'revision');
  const currentID = optionalString(raw, 'currentRevisionId', 'current_revision_id');
  return {
    id: requireString(raw, 'id'),
    slug: requireString(raw, 'slug'),
    name: requireString(raw, 'name'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId', 'owner_id'),
    ownerName: optionalString(raw, 'ownerName', 'owner_name'),
    currentRevision: current ? normalizeRevision(current) : revisions?.find((revision) => revision.id === currentID) ?? revisions?.[0],
    revisions,
    updatedAt: optionalString(raw, 'updatedAt', 'updated_at'),
  };
}

function normalizeHost(raw: LooseRecord): EnvironmentHost {
  return {
    name: requireString(raw, 'name'),
    address: requireString(raw, 'address'),
    groups: requireStringArray(raw, 'groups'),
    port: optionalNumber(raw, 'port'),
    user: optionalString(raw, 'user'),
  };
}

function normalizeCredential(raw: LooseRecord): CredentialRef {
  return {
    name: requireString(raw, 'name'),
    type: requireEnum(raw, CREDENTIAL_TYPES, 'type', 'kind'),
    reference: optionalString(raw, 'reference'),
    maskedReference: optionalString(raw, 'maskedReference'),
  };
}

function normalizeEnvironmentRevision(raw: LooseRecord): EnvironmentRevision {
  return {
    id: requireString(raw, 'id'),
    environmentId: requireString(raw, 'environmentId', 'environment_id'),
    revision: requireNumber(raw, 'revision'),
    facts: requireRecord(raw.facts, 'facts'),
    hosts: requireRecords(raw, 'hosts').map(normalizeHost),
    parameters: requireRecord(raw.parameters, 'parameters'),
    credentialRefs: requireRecords(raw, 'credentialRefs').map(normalizeCredential),
    maxConcurrentRuns: optionalNumber(raw, 'maxConcurrentRuns', 'max_concurrent_runs', 'maxConcurrent'),
    createdAt: optionalString(raw, 'createdAt'),
  };
}

function normalizeEnvironment(raw: LooseRecord): Environment {
  const revision = optionalRecord(raw, 'currentRevision', 'current_revision', 'revision');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId', 'owner_id'),
    ownerName: optionalString(raw, 'ownerName', 'owner_name'),
    status: optionalEnum(raw, ['ready', 'locked', 'offline'] as const, 'status'),
    activeRunId: optionalString(raw, 'activeRunId'),
    currentRevision: revision ? normalizeEnvironmentRevision(revision) : undefined,
    updatedAt: optionalString(raw, 'updatedAt', 'updated_at'),
  };
}

function normalizeRunStep(raw: LooseRecord): RunStep {
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    componentName: optionalString(raw, 'componentName'),
    action: optionalString(raw, 'action'),
    status: requireEnum(raw, STEP_STATUSES, 'status'),
    startedAt: optionalString(raw, 'startedAt'),
    finishedAt: optionalString(raw, 'finishedAt'),
    summary: optionalString(raw, 'summary'),
  };
}

function normalizeApproval(raw: LooseRecord): Approval {
  return {
    id: requireString(raw, 'id'),
    runId: requireString(raw, 'runId'),
    status: requireEnum(raw, ['pending', 'approved', 'rejected'] as const, 'status'),
    riskReason: optionalString(raw, 'riskReason', 'reason'),
    requestedAt: optionalString(raw, 'requestedAt'),
    decidedAt: optionalString(raw, 'decidedAt'),
  };
}

function normalizeRun(raw: LooseRecord): Run {
  const source = optionalRecord(raw, 'run') ?? raw;
  const logTail = optionalStringArray(source, 'logTail', 'log_tail');
  const steps = optionalRecords(source, 'steps')?.map(normalizeRunStep);
  const approval = optionalRecord(source, 'approval');
  return {
    id: requireString(source, 'id'),
    kind: optionalEnum(source, ['component_test', 'scenario_test', 'scenario_run'] as const, 'kind'),
    name: optionalString(source, 'name'),
    status: requireEnum(source, RUN_STATUSES, 'status'),
    scenarioId: optionalString(source, 'scenarioId'),
    scenarioName: optionalString(source, 'scenarioName'),
    scenarioRevisionId: optionalString(source, 'scenarioRevisionId'),
    componentId: optionalString(source, 'componentId'),
    componentName: optionalString(source, 'componentName'),
    componentReleaseId: optionalString(source, 'componentReleaseId'),
    environmentId: requireString(source, 'environmentId', 'environment_id'),
    environmentName: optionalString(source, 'environmentName'),
    createdBy: optionalString(source, 'createdBy', 'requestedBy'),
    createdByName: optionalString(source, 'createdByName'),
    destructive: optionalBoolean(source, 'destructive'),
    queuePosition: optionalNumber(source, 'queuePosition'),
    progress: optionalNumber(source, 'progress'),
    steps,
    approval: approval ? normalizeApproval(approval) : undefined,
    logTail,
    createdAt: optionalString(source, 'createdAt'),
    startedAt: optionalString(source, 'startedAt'),
    finishedAt: optionalString(source, 'finishedAt'),
  };
}

function normalizeNotification(raw: LooseRecord): Notification {
  const payload = optionalRecord(raw, 'payload');
  const payloadString = (key: string) => payload ? optionalString(payload, key) : undefined;
  const payloadBoolean = (key: string) => payload ? optionalBoolean(payload, key) : undefined;
  const payloadStrings = (key: string) => payload ? optionalStringArray(payload, key) : undefined;
  return {
    id: requireString(raw, 'id'),
    userId: optionalString(raw, 'userId'),
    type: optionalString(raw, 'type'),
    title: requireString(raw, 'title'),
    message: requireString(raw, 'message', 'body'),
    read: requireBoolean(raw, 'read'),
    resourceUrl: optionalString(raw, 'resourceUrl'),
    componentId: optionalString(raw, 'componentId') ?? payloadString('componentId'),
    componentName: optionalString(raw, 'componentName') ?? payloadString('componentName'),
    oldVersion: optionalString(raw, 'oldVersion') ?? payloadString('oldVersion'),
    newVersion: optionalString(raw, 'newVersion') ?? payloadString('newVersion'),
    breaking: optionalBoolean(raw, 'breaking') ?? payloadBoolean('breaking'),
    impactPaths: narrowStringPaths(field(raw, 'impactPaths') ?? field(payload ?? {}, 'impactPaths', 'paths')),
    scenarioIds: optionalStringArray(raw, 'scenarioIds') ?? payloadStrings('scenarioIds'),
    createdAt: optionalString(raw, 'createdAt'),
  };
}

function normalizeUser(value: unknown): User {
  const raw = requireRecord(value, 'session user');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    role: requireEnum(raw, ['component_owner', 'scenario_owner', 'environment_owner'] as const, 'role'),
    title: optionalString(raw, 'title'),
  };
}

function normalizeOptionalData(value: unknown): unknown {
  const data = unwrap(value);
  if (data === undefined) throw invalidResponse(200, '平台 API 缺少响应数据。');
  return data;
}

function narrowPeople(value: unknown): Array<{ id: string; name: string }> {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => isRecord(item) && typeof item.id === 'string' && typeof item.name === 'string'
    ? [{ id: item.id, name: item.name }]
    : []);
}

function narrowStringPaths(value: unknown): string[][] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((path) => Array.isArray(path) && path.every((part) => typeof part === 'string') ? [path] : []);
}

const get = <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal });
const post = <T>(path: string, body?: unknown) =>
  request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
const put = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PUT', body: JSON.stringify(body) });
const patch = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PATCH', body: JSON.stringify(body) });

export const api = {
  async me() {
    return normalizeUser(unwrap(await get<unknown>('/session/me')));
  },
  async switchUser(userId: string) {
    return normalizeUser(unwrap(await post<unknown>('/session/switch', { userId })));
  },
  async components(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/components', signal)).map((item) => normalizeComponent(requireRecord(item, 'component')));
  },
  async component(id: string) {
    return normalizeComponent(requireRecord(unwrap(await get<unknown>(`/components/${id}`)), 'component'));
  },
  async createComponent(input: Partial<Component>) {
    return normalizeComponent(requireRecord(normalizeOptionalData(await post<unknown>('/components', input)), 'component'));
  },
  async updateComponent(id: string, input: Partial<Component>) {
    return normalizeComponent(requireRecord(normalizeOptionalData(await patch<unknown>(`/components/${id}`, input)), 'component'));
  },
  async cloneRelease(releaseId: string, input: { version: string; releaseNotes: string; breaking: boolean }) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/clone`, input));
  },
  async createRelease(componentId: string, input: Partial<ComponentRelease>) {
    return normalizeReleaseActionResponse(await post<unknown>(`/components/${componentId}/releases`, input), componentId);
  },
  async updateRelease(releaseId: string, input: Partial<ComponentRelease>) {
    return normalizeReleaseActionResponse(await put<unknown>(`/component-releases/${releaseId}`, input));
  },
  async releaseImpact(releaseId: string): Promise<ImpactPreview> {
    const raw = unwrap(await get<unknown>(`/component-releases/${releaseId}/impact`));
    if (!isRecord(raw)) throw invalidResponse(200, '平台 API 返回了无效的影响分析响应。');
    return {
      componentOwners: narrowPeople(raw.componentOwners),
      scenarioOwners: narrowPeople(raw.scenarioOwners),
      scenarios: narrowPeople(raw.scenarios),
      paths: narrowStringPaths(raw.paths),
    };
  },
  async publishRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/publish`));
  },
  async deprecateRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/deprecate`));
  },
  async testRelease(releaseId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/component-releases/${releaseId}/test-runs`, { environmentId, runInput })), 'run'));
  },
  async scenarios(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/scenarios', signal)).map((item) => normalizeScenario(requireRecord(item, 'scenario')));
  },
  async createScenario(input: Pick<Scenario, 'name'> & Partial<Scenario>) {
    return normalizeScenario(requireRecord(unwrap(await post<unknown>('/scenarios', input)), 'scenario'));
  },
  async scenario(id: string) {
    return normalizeScenario(requireRecord(unwrap(await get<unknown>(`/scenarios/${id}`)), 'scenario'));
  },
  async cloneScenarioRevision(scenarioId: string) {
    return normalizeRevision(requireRecord(unwrap(await post<unknown>(`/scenarios/${scenarioId}/revisions`)), 'scenario revision'));
  },
  async saveGraph(revisionId: string, graph: { nodes: ScenarioNode[]; edges: ScenarioEdge[]; executionPolicy?: Record<string, unknown> }) {
    const backendGraph = {
      nodes: graph.nodes.map((node) => ({
        id: node.id,
        name: node.data.label,
        releaseId: node.data.releaseId,
        action: node.data.action,
        hostGroup: node.data.hostGroup,
        values: node.data.values ?? {},
        bindings: node.data.bindings ?? {},
        runInputs: node.data.runInputs ?? [],
        position: node.position,
      })),
      edges: graph.edges,
      executionPolicy: graph.executionPolicy ?? {},
    };
    return normalizeRevision(requireRecord(normalizeOptionalData(await put<unknown>(`/scenario-revisions/${revisionId}/graph`, backendGraph)), 'scenario revision'));
  },
  async validateScenario(revisionId: string) {
    const result = requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/validate`)), 'scenario validation');
    return { valid: requireBoolean(result, 'valid'), errors: requireStringArray(result, 'errors') };
  },
  async testScenario(revisionId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/test-runs`, { environmentId, runInput })), 'run'));
  },
  async runScenario(revisionId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/runs`, { environmentId, runInput })), 'run'));
  },
  async publishScenario(revisionId: string) {
    return normalizeRevision(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/publish`)), 'scenario revision'));
  },
  async deprecateScenario(revisionId: string) {
    return normalizeRevision(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/deprecate`)), 'scenario revision'));
  },
  async environments(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/environments', signal)).map((item) => normalizeEnvironment(requireRecord(item, 'environment')));
  },
  async createEnvironment(input: Partial<Environment> & { facts?: Record<string, unknown> }) {
    return normalizeEnvironment(requireRecord(unwrap(await post<unknown>('/environments', input)), 'environment'));
  },
  async updateInventory(environmentId: string, hosts: EnvironmentHost[]) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/inventory`, { hosts })), 'environment'));
  },
  async updateParameters(environmentId: string, parameters: Record<string, unknown>) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/parameters`, { parameters })), 'environment'));
  },
  async updateFacts(environmentId: string, facts: Record<string, unknown>) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/facts`, { facts })), 'environment'));
  },
  async updateCredentialRefs(environmentId: string, credentialRefs: unknown[]) {
    const refs = credentialRefs.map((credential) => {
      const item = requireRecord(credential, 'credential reference');
      return {
        name: requireString(item, 'name'),
        kind: requireEnum(item, CREDENTIAL_TYPES, 'kind', 'type'),
        reference: optionalString(item, 'reference'),
      };
    });
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/credential-refs`, {
      credentialRefs: refs,
    })), 'environment'));
  },
  async runs(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/runs', signal)).map((item) => normalizeRun(requireRecord(item, 'run')));
  },
  async run(id: string, signal?: AbortSignal) {
    return normalizeRun(requireRecord(unwrap(await get<unknown>(`/runs/${id}`, signal)), 'run'));
  },
  async cancelRun(id: string) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/runs/${id}/cancel`)), 'run'));
  },
  async approve(id: string) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/approvals/${id}/approve`)), 'run'));
  },
  async reject(id: string) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/approvals/${id}/reject`)), 'run'));
  },
  async notifications(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/notifications', signal)).map((item) => normalizeNotification(requireRecord(item, 'notification')));
  },
  async markNotificationRead(id: string, read = true) {
    return normalizeNotification(requireRecord(normalizeOptionalData(await patch<unknown>(`/notifications/${id}`, { read })), 'notification'));
  },
};

export function eventsURL(): string {
  return `${API_ROOT}/events`;
}
