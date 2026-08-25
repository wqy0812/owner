import type {
  ActionDefinition,
  Approval,
  AuditEvent,
  CandidateReleaseSet,
  Component,
  ComponentDependency,
  ComponentArtifact,
  ComponentImageBuild,
  ComponentRelease,
  ComponentTestPlan,
  ComponentTestRequest,
  CredentialRef,
  Environment,
  EnvironmentHealthCheck,
  EnvironmentHost,
  EnvironmentRollbackPlan,
  EnvironmentRevision,
  ImpactPreview,
  Notification,
  PlaybookFile,
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
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  headers.set('Accept', 'application/json');

  let response: Response;
  try {
    response = await fetch(`${API_ROOT}${path}`, {
      ...init,
      headers,
      credentials: 'include',
      cache: 'no-store',
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
  const body = record(value);
  const items = body?.items;
  if (Array.isArray(items)) return items;
  throw invalidResponse(200, '平台 API 返回了无效的列表响应。');
}

function unwrap(value: unknown): unknown {
  const body = record(value);
  if (!body || !('data' in body)) throw invalidResponse(200, '平台 API 缺少 data 响应信封。');
  return body.data;
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
const IMAGE_BUILD_STATUSES = ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'] as const;

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

function field(raw: LooseRecord, key: string): unknown {
  const value = raw[key];
  return value === null ? undefined : value;
}

function optionalString(raw: LooseRecord, key: string): string | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  if (typeof value === 'string') return value;
  throw invalidResponse(200, `平台 API 的 ${key} 必须是字符串。`);
}

function requireString(raw: LooseRecord, key: string): string {
  const value = optionalString(raw, key);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${key}。`);
}

function optionalNumber(raw: LooseRecord, key: string): number | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  throw invalidResponse(200, `平台 API 的 ${key} 必须是数字。`);
}

function requireNumber(raw: LooseRecord, key: string): number {
  const value = optionalNumber(raw, key);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${key}。`);
}

function optionalBoolean(raw: LooseRecord, key: string): boolean | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  if (typeof value === 'boolean') return value;
  throw invalidResponse(200, `平台 API 的 ${key} 必须是布尔值。`);
}

function requireBoolean(raw: LooseRecord, key: string): boolean {
  const value = optionalBoolean(raw, key);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${key}。`);
}

function optionalRecord(raw: LooseRecord, key: string): LooseRecord | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  return requireRecord(value, key);
}

function requireRecords(raw: LooseRecord, fieldName: string): LooseRecord[] {
  const value = raw[fieldName];
  if (!Array.isArray(value)) throw invalidResponse(200, `平台 API 的 ${fieldName} 必须是数组。`);
  return value.map((item, index) => requireRecord(item, `${fieldName}[${index}]`));
}

function optionalRecords(raw: LooseRecord, key: string): LooseRecord[] | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw invalidResponse(200, `平台 API 的 ${key} 必须是数组。`);
  return value.map((item, index) => requireRecord(item, `${key}[${index}]`));
}

function optionalStringArray(raw: LooseRecord, key: string): string[] | undefined {
  const value = field(raw, key);
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || !value.every((item) => typeof item === 'string')) {
    throw invalidResponse(200, `平台 API 的 ${key} 必须是字符串数组。`);
  }
  return value;
}

function requireStringArray(raw: LooseRecord, key: string): string[] {
  const value = optionalStringArray(raw, key);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${key}。`);
}

function optionalEnum<T extends string>(raw: LooseRecord, values: readonly T[], key: string): T | undefined {
  const value = optionalString(raw, key);
  if (value === undefined) return undefined;
  if ((values as readonly string[]).includes(value)) return value as T;
  throw invalidResponse(200, `平台 API 的 ${key} 值 ${JSON.stringify(value)} 不受支持。`);
}

function requireEnum<T extends string>(raw: LooseRecord, values: readonly T[], key: string): T {
  const value = optionalEnum(raw, values, key);
  if (value !== undefined) return value;
  throw invalidResponse(200, `平台 API 缺少必填字段 ${key}。`);
}

function optionalObject(raw: LooseRecord, key: string): Record<string, unknown> | undefined {
  return optionalRecord(raw, key);
}

function normalizeAction(raw: LooseRecord): ActionDefinition {
  const riskLevel = optionalEnum(raw, ['low', 'medium', 'high', 'destructive'] as const, 'riskLevel');
  const destructive = optionalBoolean(raw, 'destructive');
  return {
    id: optionalString(raw, 'id'),
    name: optionalString(raw, 'name'),
    type: requireEnum(raw, ACTION_TYPES, 'kind'),
    playbook: requireString(raw, 'playbook'),
    tags: optionalStringArray(raw, 'tags'),
    limit: optionalString(raw, 'limit'),
    hostGroup: optionalString(raw, 'hostGroup'),
    timeoutSeconds: optionalNumber(raw, 'timeoutSeconds'),
    allowedParameters: optionalStringArray(raw, 'allowedParameters'),
    requiredCredentials: optionalStringArray(raw, 'requiredCredentials'),
    riskLevel,
    destructive,
    idempotent: optionalBoolean(raw, 'idempotent'),
    fromReleaseId: optionalString(raw, 'fromReleaseId'),
    toReleaseId: optionalString(raw, 'toReleaseId'),
  };
}

function normalizeParameter(raw: LooseRecord): import('../types/domain').ParameterDefinition {
  const type = requireEnum(raw, ['string', 'boolean', 'integer', 'number', 'object', 'array'] as const, 'type');
  const visibility = requireEnum(raw, ['internal', 'public'] as const, 'visibility');
  const enumValues = raw.enum;
  return {
    name: requireString(raw, 'name'),
    description: requireString(raw, 'description'),
    type,
    required: optionalBoolean(raw, 'required') ?? false,
    defaultValue: raw.defaultValue,
    visibility,
    enum: Array.isArray(enumValues) ? enumValues : undefined,
    minLength: optionalNumber(raw, 'minLength'),
  };
}

function normalizeArtifact(raw: LooseRecord): ComponentArtifact {
  return {
    id: requireString(raw, 'id'),
    releaseId: requireString(raw, 'releaseId'),
    alias: requireString(raw, 'alias'),
    fileStation: requireString(raw, 'fileStation'),
    relativePath: requireString(raw, 'relativePath'),
    filename: requireString(raw, 'filename'),
    sha256: requireString(raw, 'sha256'),
    sizeBytes: requireNumber(raw, 'sizeBytes'),
    sourceMode: requireEnum(raw, ['upload', 'register'] as const, 'sourceMode'),
    environmentId: requireString(raw, 'environmentId'),
    environmentRevisionId: requireString(raw, 'environmentRevisionId'),
    createdBy: requireString(raw, 'createdBy'),
    createdAt: requireString(raw, 'createdAt'),
  };
}

function normalizeDependency(raw: LooseRecord): ComponentDependency {
  const mappings = optionalRecords(raw, 'parameterMappings') ?? [];
  return {
    id: optionalString(raw, 'id'),
    componentId: requireString(raw, 'upstreamComponentId'),
    componentName: optionalString(raw, 'upstreamComponentName'),
    releaseId: requireString(raw, 'upstreamReleaseId'),
    version: optionalString(raw, 'upstreamVersion'),
    purpose: optionalString(raw, 'purpose'),
    parameterMappings: mappings.map((item) => ({
      upstreamParameter: requireString(item, 'upstreamParameter'),
      targetParameter: requireString(item, 'targetParameter'),
    })),
  };
}

function normalizeRelease(raw: LooseRecord): ComponentRelease {
  const state = requireEnum(raw, RELEASE_STATES, 'status');
  const actions = optionalRecords(raw, 'actions')?.map(normalizeAction);
  const dependencies = optionalRecords(raw, 'dependencies')?.map(normalizeDependency);
  return {
    id: requireString(raw, 'id'),
    componentId: requireString(raw, 'componentId'),
    version: requireString(raw, 'version'),
    type: optionalEnum(raw, ['atomic', 'bundle'] as const, 'type'),
    state,
    verified: optionalBoolean(raw, 'verified'),
    candidate: optionalBoolean(raw, 'candidate'),
    breaking: optionalBoolean(raw, 'breaking'),
    releaseNotes: optionalString(raw, 'releaseNotes'),
    dependencies,
    environmentConstraints: optionalObject(raw, 'environmentConstraints'),
    parameters: optionalRecords(raw, 'parameters')?.map(normalizeParameter) ?? [],
    actions,
    artifacts: optionalRecords(raw, 'artifacts')?.map(normalizeArtifact) ?? [],
    createdAt: optionalString(raw, 'createdAt'),
    releasedAt: optionalString(raw, 'releasedAt'),
  };
}

function normalizeReleaseActionResponse(value: unknown): ComponentRelease {
  const raw = requireRecord(normalizeOptionalData(value), 'release');
  return normalizeRelease(raw);
}

function normalizeImageBuild(raw: LooseRecord): ComponentImageBuild {
  const logs = optionalRecords(raw, 'logs')?.map((log) => ({
    id: requireNumber(log, 'id'),
    buildId: requireString(log, 'buildId'),
    stream: requireEnum(log, ['stdout', 'stderr', 'system'] as const, 'stream'),
    message: requireString(log, 'message'),
    createdAt: requireString(log, 'createdAt'),
  }));
  return {
    id: requireString(raw, 'id'),
    releaseId: requireString(raw, 'releaseId'),
    environmentId: optionalString(raw, 'environmentId'),
    environmentRevisionId: optionalString(raw, 'environmentRevisionId'),
    requestedBy: requireString(raw, 'requestedBy'),
    status: requireEnum(raw, IMAGE_BUILD_STATUSES, 'status'),
    dockerfileSha256: requireString(raw, 'dockerfileSha256'),
    imageTag: requireString(raw, 'imageTag'),
    imageRef: requireString(raw, 'imageRef'),
    imageDigest: optionalString(raw, 'imageDigest'),
    error: optionalString(raw, 'error'),
    createdAt: requireString(raw, 'createdAt'),
    startedAt: optionalString(raw, 'startedAt'),
    finishedAt: optionalString(raw, 'finishedAt'),
    logs,
  };
}

function normalizePlaybook(raw: LooseRecord): PlaybookFile {
  return {
    path: requireString(raw, 'path'),
    filename: requireString(raw, 'filename'),
    content: requireString(raw, 'content'),
    sha256: requireString(raw, 'sha256'),
    updatedAt: optionalString(raw, 'updatedAt'),
  };
}

function normalizeComponent(raw: LooseRecord): Component {
  const releases = optionalRecords(raw, 'releases')?.map((release) => normalizeRelease(release));
  const latest = optionalRecord(raw, 'latestRelease');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    slug: optionalString(raw, 'slug'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId'),
    ownerName: optionalString(raw, 'ownerName'),
    layer: requireEnum(raw, COMPONENT_LAYERS, 'layer'),
    category: requireEnum(raw, COMPONENT_CATEGORIES, 'category'),
    kind: requireEnum(raw, COMPONENT_KINDS, 'kind'),
    requiredness: requireEnum(raw, COMPONENT_REQUIREDNESS, 'requiredness'),
    latestRelease: latest ? normalizeRelease(latest) : releases?.[0],
    releases,
    releaseCount: optionalNumber(raw, 'releaseCount'),
    updatedAt: optionalString(raw, 'updatedAt'),
  };
}

function normalizePosition(raw: LooseRecord): { x: number; y: number } {
  return { x: requireNumber(raw, 'x'), y: requireNumber(raw, 'y') };
}

function normalizeScenarioNode(raw: LooseRecord): ScenarioNode {
  const data = requireRecord(raw.data, 'node.data');
  return {
    id: requireString(raw, 'id'),
    type: requireEnum(raw, ['component'] as const, 'type'),
    position: normalizePosition(requireRecord(raw.position, 'node.position')),
    data: {
      label: requireString(data, 'label'),
      componentId: requireString(data, 'componentId'),
      releaseId: requireString(data, 'releaseId'),
      version: optionalString(data, 'version'),
      action: optionalEnum(data, ACTION_TYPES, 'action'),
      hostGroup: optionalString(data, 'hostGroup'),
      values: optionalObject(data, 'values'),
      runInputs: optionalStringArray(data, 'runInputs'),
      dependencySources: optionalRecord(data, 'dependencySources') as Record<string, string> | undefined,
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
    scenarioId: requireString(raw, 'scenarioId'),
    revision: requireNumber(raw, 'revision'),
    state: requireEnum(raw, ['draft', 'testing', 'test_passed', 'released', 'deprecated', 'abandoned'] as const, 'state'),
    nodes: requireRecords(raw, 'nodes').map(normalizeScenarioNode),
    edges: requireRecords(raw, 'edges').map(normalizeScenarioEdge),
    executionPolicy: optionalObject(raw, 'executionPolicy'),
    testedAt: optionalString(raw, 'testPassedAt'),
    createdAt: optionalString(raw, 'createdAt'),
  };
}

function normalizeScenario(raw: LooseRecord): Scenario {
  const revisions = optionalRecords(raw, 'revisions')?.map(normalizeRevision);
  const current = optionalRecord(raw, 'currentRevision');
  const currentID = optionalString(raw, 'currentRevisionId');
  return {
    id: requireString(raw, 'id'),
    slug: requireString(raw, 'slug'),
    name: requireString(raw, 'name'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId'),
    ownerName: optionalString(raw, 'ownerName'),
    currentRevisionId: currentID,
    currentRevision: current ? normalizeRevision(current) : undefined,
    revisions,
    updatedAt: optionalString(raw, 'updatedAt'),
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
    type: requireEnum(raw, CREDENTIAL_TYPES, 'kind'),
    reference: optionalString(raw, 'reference'),
    maskedReference: optionalString(raw, 'maskedReference'),
  };
}

function normalizeEnvironmentRevision(raw: LooseRecord): EnvironmentRevision {
  const variableValues = optionalRecord(raw, 'variables') ?? {};
  const variables = Object.fromEntries(Object.entries(variableValues).map(([name, value]) => {
    if (typeof value !== 'string') throw invalidResponse(200, `环境变量 ${name} 不是字符串。`);
    return [name, value];
  }));
  return {
    id: requireString(raw, 'id'),
    environmentId: requireString(raw, 'environmentId'),
    revision: requireNumber(raw, 'revision'),
    facts: requireRecord(raw.facts, 'facts'),
    hosts: requireRecords(raw, 'hosts').map(normalizeHost),
    variables,
    credentialRefs: requireRecords(raw, 'credentialRefs').map(normalizeCredential),
    maxConcurrentRuns: optionalNumber(raw, 'maxConcurrent'),
    createdBy: optionalString(raw, 'createdBy'),
    changeReason: optionalString(raw, 'changeReason'),
    createdAt: optionalString(raw, 'createdAt'),
  };
}

function normalizeEnvironmentHealthCheck(raw: LooseRecord): EnvironmentHealthCheck {
  return {
    id: requireString(raw, 'id'),
    environmentId: requireString(raw, 'environmentId'),
    environmentRevisionId: requireString(raw, 'environmentRevisionId'),
    status: requireEnum(raw, ['healthy', 'degraded'] as const, 'status'),
    results: requireRecords(raw, 'results').map((item) => ({
      kind: requireEnum(item, ['host', 'dependency', 'configuration'] as const, 'kind'),
      name: requireString(item, 'name'),
      address: requireString(item, 'address'),
      reachable: requireBoolean(item, 'reachable'),
      latencyMs: requireNumber(item, 'latencyMs'),
      error: optionalString(item, 'error'),
    })),
    checkedAt: requireString(raw, 'checkedAt'),
  };
}

function normalizeEnvironment(raw: LooseRecord): Environment {
  const revision = optionalRecord(raw, 'currentRevision');
  const revisions = optionalRecords(raw, 'revisions')?.map(normalizeEnvironmentRevision);
  const healthCheck = optionalRecord(raw, 'healthCheck');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId'),
    ownerName: optionalString(raw, 'ownerName'),
    status: optionalEnum(raw, ['ready', 'locked', 'offline'] as const, 'status'),
    schedulingStatus: optionalEnum(raw, ['idle', 'queued', 'awaiting_approval', 'running'] as const, 'schedulingStatus'),
    activeRunId: optionalString(raw, 'activeRunId'),
    currentRevision: revision ? normalizeEnvironmentRevision(revision) : undefined,
    revisions,
    healthCheck: healthCheck ? normalizeEnvironmentHealthCheck(healthCheck) : undefined,
    updatedAt: optionalString(raw, 'updatedAt'),
  };
}

function normalizeAuditEvent(raw: LooseRecord): AuditEvent {
  return {
    id: requireString(raw, 'id'),
    actorId: requireString(raw, 'actorId'),
    action: requireString(raw, 'action'),
    resourceType: requireString(raw, 'resourceType'),
    resourceId: requireString(raw, 'resourceId'),
    metadata: optionalRecord(raw, 'metadata') ?? {},
    createdAt: requireString(raw, 'createdAt'),
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
    riskReason: optionalString(raw, 'riskReason'),
    requestedAt: optionalString(raw, 'requestedAt'),
    decidedAt: optionalString(raw, 'decidedAt'),
  };
}

function normalizeRun(raw: LooseRecord): Run {
  const source = raw;
  const logTail = optionalStringArray(source, 'logTail');
  const steps = optionalRecords(source, 'steps')?.map(normalizeRunStep);
  const approval = optionalRecord(source, 'approval');
  const backups = optionalRecords(source, 'backups')?.map((backup) => ({
    nodeId: optionalString(backup, 'nodeId'),
    componentId: requireString(backup, 'componentId'),
    componentName: optionalString(backup, 'componentName'),
    releaseId: requireString(backup, 'releaseId'),
    action: requireString(backup, 'action'),
    backupRef: requireString(backup, 'backupRef'),
    installRunId: requireString(backup, 'installRunId'),
    capturedAt: requireString(backup, 'capturedAt'),
    playbookSha256: requireString(backup, 'playbookSha256'),
  }));
  const artifactTransfers = optionalRecords(source, 'artifactTransfers')?.map((item) => ({
    alias: requireString(item, 'alias'), sourceStation: requireString(item, 'sourceStation'), targetStation: requireString(item, 'targetStation'),
    relativePath: requireString(item, 'relativePath'), sha256: requireString(item, 'sha256'),
  }));
  const imageTransfers = optionalRecords(source, 'imageTransfers')?.map((item) => ({
    sourceRegistry: requireString(item, 'sourceRegistry'), targetRegistry: requireString(item, 'targetRegistry'),
    sourceDigest: requireString(item, 'sourceDigest'), targetDigest: requireString(item, 'targetDigest'),
  }));
  return {
    id: requireString(source, 'id'),
    kind: optionalEnum(source, ['component_test', 'scenario_test', 'scenario_run', 'environment_rollback'] as const, 'kind'),
    name: optionalString(source, 'name'),
    status: requireEnum(source, RUN_STATUSES, 'status'),
    scenarioId: optionalString(source, 'scenarioId'),
    scenarioName: optionalString(source, 'scenarioName'),
    scenarioRevisionId: optionalString(source, 'scenarioRevisionId'),
    componentId: optionalString(source, 'componentId'),
    componentName: optionalString(source, 'componentName'),
    componentReleaseId: optionalString(source, 'componentReleaseId'),
    action: optionalString(source, 'action'),
    environmentId: requireString(source, 'environmentId'),
    environmentName: optionalString(source, 'environmentName'),
    createdBy: optionalString(source, 'requestedBy'),
    createdByName: optionalString(source, 'createdByName'),
    destructive: optionalBoolean(source, 'destructive'),
    queuePosition: optionalNumber(source, 'queuePosition'),
    progress: optionalNumber(source, 'progress'),
    steps,
    approval: approval ? normalizeApproval(approval) : undefined,
    logTail,
    resolvedParametersByNode: optionalRecord(source, 'resolvedParametersByNode') as Run['resolvedParametersByNode'],
    backups,
    artifactTransfers,
    imageTransfers,
    createdAt: optionalString(source, 'createdAt'),
    startedAt: optionalString(source, 'startedAt'),
    finishedAt: optionalString(source, 'finishedAt'),
  };
}

function normalizeComponentTestPlan(raw: LooseRecord): ComponentTestPlan {
  const source = raw;
  const steps = optionalRecords(source, 'steps')?.map((step) => ({
    order: requireNumber(step, 'order'),
    componentId: requireString(step, 'componentId'),
    componentName: requireString(step, 'componentName'),
    releaseId: requireString(step, 'releaseId'),
    releaseVersion: requireString(step, 'releaseVersion'),
    action: requireEnum(step, ACTION_TYPES, 'action'),
    playbook: requireString(step, 'playbook'),
    limit: optionalString(step, 'limit'),
    needsApproval: requireBoolean(step, 'needsApproval'),
    fromReleaseId: optionalString(step, 'fromReleaseId'),
    fromReleaseVersion: optionalString(step, 'fromReleaseVersion'),
    toReleaseId: optionalString(step, 'toReleaseId'),
    toReleaseVersion: optionalString(step, 'toReleaseVersion'),
    backupRef: optionalString(step, 'backupRef'),
    backupInstallRunId: optionalString(step, 'backupInstallRunId'),
    backupCapturedAt: optionalString(step, 'backupCapturedAt'),
    backupPlaybookSha256: optionalString(step, 'backupPlaybookSha256'),
  })) ?? [];
  return {
    environmentId: requireString(source, 'environmentId'),
    environmentRevisionId: requireString(source, 'environmentRevisionId'),
    destructive: requireBoolean(source, 'destructive'),
    requiresApproval: requireBoolean(source, 'requiresApproval'),
    planDigest: requireString(source, 'planDigest'),
    steps,
  };
}

function normalizeEnvironmentRollbackPlan(raw: LooseRecord): EnvironmentRollbackPlan {
  return {
    ...normalizeComponentTestPlan(raw),
    environmentName: requireString(raw, 'environmentName'),
    sources: requireRecords(raw, 'sources').map((source) => ({
      runId: requireString(source, 'runId'),
      kind: requireEnum(source, ['component_test', 'scenario_test', 'scenario_run', 'environment_rollback'] as const, 'kind'),
      scenarioRevisionId: optionalString(source, 'scenarioRevisionId'),
      componentCount: requireNumber(source, 'componentCount'),
    })),
    componentCount: requireNumber(raw, 'componentCount'),
    nodeCount: requireNumber(raw, 'nodeCount'),
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
    message: requireString(raw, 'body'),
    read: requireBoolean(raw, 'read'),
    resourceUrl: optionalString(raw, 'resourceUrl'),
    componentId: payloadString('componentId'),
    componentName: payloadString('componentName'),
    oldVersion: payloadString('oldVersion'),
    newVersion: payloadString('newVersion'),
    breaking: payloadBoolean('breaking'),
    impactPaths: narrowStringPaths(field(payload ?? {}, 'paths')),
    scenarioIds: payloadStrings('scenarioIds'),
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

function serializeAction(action: ActionDefinition) {
  return {
    id: action.id,
    name: action.name,
    kind: action.type,
    playbook: action.playbook,
    tags: action.tags,
    limit: action.limit,
    hostGroup: action.hostGroup,
    timeoutSeconds: action.timeoutSeconds,
    allowedParameters: action.allowedParameters,
    requiredCredentials: action.requiredCredentials,
    riskLevel: action.riskLevel ?? (action.destructive ? 'destructive' : 'low'),
    destructive: action.destructive ?? action.riskLevel === 'destructive',
    idempotent: action.idempotent ?? false,
    fromReleaseId: action.fromReleaseId,
    toReleaseId: action.toReleaseId,
  };
}

function serializeDependency(dependency: ComponentDependency) {
  return {
    upstreamComponentId: dependency.componentId,
    upstreamReleaseId: dependency.releaseId,
    purpose: dependency.purpose,
    parameterMappings: dependency.parameterMappings ?? [],
  };
}

function serializeRelease(input: Partial<ComponentRelease>) {
  return {
    version: input.version,
    type: input.type,
    status: input.state,
    releaseNotes: input.releaseNotes,
    breaking: input.breaking,
    environmentConstraints: input.environmentConstraints,
    parameters: input.parameters,
    dependencies: input.dependencies?.map(serializeDependency),
    actions: input.actions?.map(serializeAction),
  };
}

const get = <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal });
const post = <T>(path: string, body?: unknown) =>
  request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
const postForm = <T>(path: string, body: FormData) => request<T>(path, { method: 'POST', body });
const put = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PUT', body: JSON.stringify(body) });
const patch = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PATCH', body: JSON.stringify(body) });

export const api = {
  async sessionUsers() {
    return unwrapList(await get<unknown>('/session/users')).map(normalizeUser);
  },
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
  async cloneRelease(releaseId: string, input: { version: string; releaseNotes: string; breaking: boolean; environmentConstraints?: Record<string, unknown> }) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/clone`, input));
  },
  async createRelease(componentId: string, input: Partial<ComponentRelease>) {
    return normalizeReleaseActionResponse(await post<unknown>(`/components/${componentId}/releases`, serializeRelease(input)));
  },
  async updateRelease(releaseId: string, input: Partial<ComponentRelease>) {
    return normalizeReleaseActionResponse(await put<unknown>(`/component-releases/${releaseId}`, serializeRelease(input)));
  },
  async updateReleaseContract(releaseId: string, input: Pick<ComponentRelease, 'parameters' | 'dependencies'>) {
    return normalizeReleaseActionResponse(await put<unknown>(`/component-releases/${releaseId}/contract`, {
      parameters: input.parameters,
      dependencies: input.dependencies?.map(serializeDependency),
    }));
  },
  async uploadArtifact(releaseId: string, input: { environmentId: string; alias: string; sha256?: string; artifact: File; checksumFile?: File }) {
    const form = new FormData();
    form.set('environmentId', input.environmentId);
    form.set('alias', input.alias);
    if (input.sha256) form.set('sha256', input.sha256);
    form.set('artifact', input.artifact, input.artifact.name);
    if (input.checksumFile) form.set('checksumFile', input.checksumFile, input.checksumFile.name);
    return normalizeArtifact(requireRecord(unwrap(await postForm<unknown>(`/component-releases/${releaseId}/artifacts/upload`, form)), 'artifact'));
  },
  async registerArtifact(releaseId: string, input: { environmentId: string; alias: string; relativePath: string; sha256: string }) {
    return normalizeArtifact(requireRecord(unwrap(await post<unknown>(`/component-releases/${releaseId}/artifacts/register`, input)), 'artifact'));
  },
  async deleteArtifact(releaseId: string, alias: string) {
    await request<unknown>(`/component-releases/${releaseId}/artifacts/${encodeURIComponent(alias)}`, { method: 'DELETE' });
  },
  async playbook(releaseId: string, path: string, signal?: AbortSignal) {
    return normalizePlaybook(requireRecord(unwrap(await get<unknown>(`/component-releases/${releaseId}/playbook?path=${encodeURIComponent(path)}`, signal)), 'Playbook'));
  },
  async savePlaybook(releaseId: string, filename: string, content: string) {
    return normalizePlaybook(requireRecord(unwrap(await put<unknown>(`/component-releases/${releaseId}/playbook`, { filename, content })), 'Playbook'));
  },
  async uploadPlaybook(releaseId: string, file: File) {
    const form = new FormData();
    form.set('playbook', file, file.name);
    return normalizePlaybook(requireRecord(unwrap(await postForm<unknown>(`/component-releases/${releaseId}/playbook`, form)), 'Playbook'));
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
  async setReleaseCandidate(releaseId: string, candidate: boolean) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/candidate`, { candidate }));
  },
  async deprecateRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/deprecate`));
  },
  async imageBuilds(releaseId: string) {
    return unwrapList(await get<unknown>(`/component-releases/${releaseId}/image-builds`)).map((item) => normalizeImageBuild(requireRecord(item, 'image build')));
  },
  async startImageBuild(releaseId: string, environmentId: string, dockerfile: File, tag: string) {
    const form = new FormData();
    form.set('dockerfile', dockerfile, dockerfile.name || 'Dockerfile');
    form.set('environmentId', environmentId);
    form.set('tag', tag);
    return normalizeImageBuild(requireRecord(unwrap(await postForm<unknown>(`/component-releases/${releaseId}/image-builds`, form)), 'image build'));
  },
  async imageBuild(id: string, signal?: AbortSignal) {
    return normalizeImageBuild(requireRecord(unwrap(await get<unknown>(`/image-builds/${id}`, signal)), 'image build'));
  },
  async previewReleaseTest(releaseId: string, input: ComponentTestRequest) {
    return normalizeComponentTestPlan(requireRecord(normalizeOptionalData(await post<unknown>(`/component-releases/${releaseId}/test-plan`, input)), 'component test plan'));
  },
  async testRelease(releaseId: string, input: ComponentTestRequest) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/component-releases/${releaseId}/test-runs`, input)), 'run'));
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
  async abandonScenarioRevision(revisionId: string) {
    return normalizeScenario(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/abandon`)), 'scenario'));
  },
  async saveGraph(revisionId: string, graph: { nodes: ScenarioNode[]; edges: ScenarioEdge[]; executionPolicy?: Record<string, unknown> }) {
    const backendGraph = {
      nodes: graph.nodes.map((node) => ({
        id: node.id,
        type: 'component',
        position: node.position,
        data: {
          label: node.data.label,
          releaseId: node.data.releaseId,
          action: node.data.action,
          hostGroup: node.data.hostGroup,
          values: node.data.values ?? {},
          runInputs: node.data.runInputs ?? [],
          dependencySources: node.data.dependencySources ?? {},
        },
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
  async candidateReleaseSet(revisionId: string): Promise<CandidateReleaseSet> {
    const raw = requireRecord(unwrap(await get<unknown>(`/scenario-revisions/${revisionId}/candidate-release-set`)), 'candidate release set');
    return {
      scenarioRevisionId: requireString(raw, 'scenarioRevisionId'),
      ready: requireBoolean(raw, 'ready'),
      releases: requireRecords(raw, 'releases').map((item) => ({
        releaseId: requireString(item, 'releaseId'),
        componentId: requireString(item, 'componentId'),
        componentName: requireString(item, 'componentName'),
        version: requireString(item, 'version'),
      })),
      issues: requireRecords(raw, 'issues').map((item) => ({
        code: requireString(item, 'code'),
        message: requireString(item, 'message'),
        nodeId: optionalString(item, 'nodeId'),
      })),
    };
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
  async updateInventory(environmentId: string, hosts: EnvironmentHost[], changeReason = '') {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/inventory`, { hosts, changeReason })), 'environment'));
  },
  async updateVariables(environmentId: string, variables: Record<string, string>, changeReason = '') {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/variables`, { variables, changeReason })), 'environment'));
  },
  async updateFacts(environmentId: string, facts: Record<string, unknown>, changeReason = '') {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/facts`, { facts, changeReason })), 'environment'));
  },
  async updateCredentialRefs(environmentId: string, credentialRefs: unknown[], changeReason = '') {
    const refs = credentialRefs.map((credential) => {
      const item = requireRecord(credential, 'credential reference');
      return {
        name: requireString(item, 'name'),
        kind: requireEnum(item, CREDENTIAL_TYPES, 'type'),
        reference: optionalString(item, 'reference'),
      };
    });
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/credential-refs`, {
      credentialRefs: refs, changeReason,
    })), 'environment'));
  },
  async checkEnvironmentHealth(environmentId: string) {
    return normalizeEnvironmentHealthCheck(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/health-checks`)), 'environment health check'));
  },
  async previewEnvironmentRollback(environmentId: string) {
    return normalizeEnvironmentRollbackPlan(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/cluster-rollback-plan`)), 'environment rollback plan'));
  },
  async startEnvironmentRollback(environmentId: string, input: { expectedPlanDigest: string; confirmEnvironmentName: string }) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/cluster-rollback-runs`, input)), 'run'));
  },
  async restoreEnvironmentRevision(environmentId: string, revisionId: string, changeReason: string) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/revisions/${revisionId}/restore`, { changeReason })), 'environment'));
  },
  async auditEvents(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/audit-events', signal)).map((item) => normalizeAuditEvent(requireRecord(item, 'audit event')));
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
  async batchDecideApprovals(approvalIds: string[], decision: 'approved' | 'rejected', reason: string) {
    const value = unwrap(await post<unknown>('/approvals/batch', { approvalIds, decision, reason }));
    if (!Array.isArray(value)) throw invalidResponse(200, '平台 API 返回了无效的批量审批结果。');
    return value.map((item) => normalizeRun(requireRecord(item, 'run')));
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
