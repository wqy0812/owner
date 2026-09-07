import { scenarioGraphContent } from '../types/scenarioGraphContent';
import type { RunActivity, RunWaitingObservation } from '../types/domain';
import type { ArchiveHealth, ArchiveInfo, CleanupItem, RetentionPolicy } from '../types/runRetention';
import type { ComponentUsage } from '../types/componentUsage';
import type {
  ComponentSummary, EvidenceSummary, ReleaseEvidenceSummary, RunPage, RunSummary,
  ActionDefinition,
  Approval,
  AuditEvent,
  CandidateReleaseSet,
  CatalogBackupResult,
  CatalogRecoveryResult,
  CatalogRepositoryStatus,
  CatalogRestorePlan,
  Component,
  ComponentDependency,
  ComponentArtifact,
  ComponentImageBuild,
  ComponentImage,
  ComponentRelease,
  ComponentTestPlan,
  ComponentTestRequest,
  DeliveryDecision,
  DeliveryRequirement,
  DeliveryResult,
  CredentialRef,
  Environment,
  EnvironmentExportDocument,
  EnvironmentHealthCheck,
  EnvironmentConnectivityCheck,
  EnvironmentSSHCheck,
  EnvironmentHost,
  EnvironmentImportPlan,
  EnvironmentLifecycle,
  EnvironmentRevisionDeletionImpact,
  EnvironmentRollbackPlan,
  EnvironmentRevision,
  EnvironmentParameterField,
  EnvironmentVariableDefinition,
  ImpactPreview,
  Notification,
  PlaybookFile,
  PlaybookWorkspace,
  PlaybookWorkspaceFile,
  ReleaseReviewPreview,
  PlatformOption,
  PlatformOptionCategory,
  Run,
  RunRetryPlan,
  RunStep,
  Scenario,
  ScenarioAcceptance, ScenarioAcceptanceJob, ScenarioParameterBinding, ScenarioForkInput, ScenarioForkPlan, ScenarioClonePlan, ScenarioExecutionRequest, ScenarioExecutionPreview, ScenarioTestEvidence,
  ScenarioEdge,
  ScenarioNode,
  ScenarioRevision,
  ScenarioParameterOverview,
  User,
  WorkAction,
  WorkExplanation,
  WorkReason,
  Workbench,
} from '../types/domain';

const API_ROOT = '/api/v1';

interface ApiErrorBody {
  error?: {
    code?: string;
    message?: string;
    details?: unknown;
    explanation?: unknown;
  };
  message?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;
  readonly explanation?: WorkExplanation;

  constructor(status: number, body: ApiErrorBody) {
    super(body.error?.message ?? body.message ?? `请求失败（HTTP ${status}）`);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.error?.code ?? `HTTP_${status}`;
    this.details = body.error?.details;
    if (body.error?.explanation !== undefined && body.error.explanation !== null) {
      try { this.explanation = normalizeWorkExplanation(body.error.explanation); } catch { /* Keep the original API error usable. */ }
    }
  }
}

export function actionableExplanation(reason: unknown): WorkExplanation | undefined {
  return reason instanceof ApiError ? reason.explanation : undefined;
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

async function download(path: string, body: unknown): Promise<Blob> {
  const response = await fetch(`${API_ROOT}${path}`, { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify(body), credentials: 'include', cache: 'no-store' });
  if (!response.ok) {
    const text = await response.text();
    const payload = text ? parseJSON(text) : undefined;
    throw new ApiError(response.status, isApiErrorBody(payload) ? payload : {});
  }
  return response.blob();
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
const ACTION_TYPES = ['check', 'inspect', 'preflight', 'install', 'configure', 'upgrade', 'verify', 'rollback', 'uninstall'] as const;
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

function normalizeResourceContract(raw: LooseRecord | undefined): ActionDefinition['resourceContract'] {
 if (!raw) return undefined;
 if (requireNumber(raw, 'version') !== 1) throw new Error('Unsupported resource contract version');
 return { version: 1, noManagedPaths: requireBoolean(raw, 'noManagedPaths'), claims: requireRecords(raw, 'claims').map(claim => ({
  id: requireString(claim, 'id'), path: requireString(claim, 'path'), scope: requireEnum(claim, ['file', 'tree'] as const, 'scope'), access: requireEnum(claim, ['manage', 'read', 'verify'] as const, 'access'),
  exclusive: optionalBoolean(claim, 'exclusive'), excludes: optionalStringArray(claim, 'excludes'), sharedPaths: optionalStringArray(claim, 'sharedPaths'),
  sharedWith: claim.sharedWith ? { releaseId: requireString(requireRecord(claim.sharedWith, 'shared resource'), 'releaseId'), claimId: requireString(requireRecord(claim.sharedWith, 'shared resource'), 'claimId') } : undefined,
 })) };
}
function normalizeAction(raw: LooseRecord): ActionDefinition {
  const riskLevel = optionalEnum(raw, ['low', 'medium', 'high', 'destructive'] as const, 'riskLevel');
  const destructive = optionalBoolean(raw, 'destructive');
  return {
    id: optionalString(raw, 'id'),
    name: optionalString(raw, 'name'),
    type: requireEnum(raw, ACTION_TYPES, 'kind'),
    resourceContract: normalizeResourceContract(optionalRecord(raw, 'resourceContract')),
    preCheckActionId: optionalString(raw, 'preCheckActionId'),
    postCheckActionId: optionalString(raw, 'postCheckActionId'),
    become: optionalBoolean(raw, 'become'),
    legacyYamlSettings: normalizeLegacyYaml(raw, optionalRecord(raw, 'resourceContract')?.checks),
    playbook: requireString(raw, 'playbook'),
    tags: optionalStringArray(raw, 'tags'),
    hostGroup: optionalString(raw, 'hostGroup'),
    timeoutSeconds: optionalNumber(raw, 'timeoutSeconds'),
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
    visibility,
    modifiable: optionalBoolean(raw, 'modifiable') ?? false,
    valueProvider: requireEnum(raw, ['component_owner', 'scenario_owner', 'environment_owner', 'upstream_mapping'] as const, 'valueProvider'),
    fixedValue: raw.fixedValue,
    suggestedValue: raw.suggestedValue,
    testValue: raw.testValue,
    environmentBinding: optionalRecord(raw, 'environmentBinding') as import('../types/domain').EnvironmentParameterBinding | undefined,
    enum: Array.isArray(enumValues) ? enumValues : undefined,
    minLength: optionalNumber(raw, 'minLength'),
  };
}

function normalizeArtifact(raw: LooseRecord): ComponentArtifact {
  return {
    id: requireString(raw, 'id'),
    releaseId: requireString(raw, 'releaseId'),
    alias: requireString(raw, 'alias'),
    filename: requireString(raw, 'filename'),
    sha256: requireString(raw, 'sha256'),
    sizeBytes: requireNumber(raw, 'sizeBytes'),
    sourceUrl: requireString(raw, 'sourceUrl'),
    sourceUpdatedBy: requireString(raw, 'sourceUpdatedBy'),
    sourceUpdatedAt: requireString(raw, 'sourceUpdatedAt'),
    createdBy: requireString(raw, 'createdBy'),
    createdAt: requireString(raw, 'createdAt'),
  };
}

function normalizeImage(raw: LooseRecord): ComponentImage {
  return {
    id: requireString(raw, 'id'),
    releaseId: requireString(raw, 'releaseId'),
    logicalName: requireString(raw, 'logicalName'),
    digest: requireString(raw, 'digest'),
    sourceRef: requireString(raw, 'sourceRef'),
    sourceUpdatedBy: requireString(raw, 'sourceUpdatedBy'),
    sourceUpdatedAt: requireString(raw, 'sourceUpdatedAt'),
    createdBy: requireString(raw, 'createdBy'),
    createdAt: requireString(raw, 'createdAt'),
  };
}

function normalizeDependency(raw: LooseRecord): ComponentDependency {
  if (raw.kind !== undefined && raw.kind !== '' && raw.kind !== 'configuration') throw new Error('Invalid dependency kind');
  const mappings = optionalRecords(raw, 'parameterMappings') ?? [];
  return {
    ...(raw.kind === 'configuration' ? { kind: 'configuration' as const } : {}),
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
    lineId: requireString(raw, 'lineId'),
    lineName: requireString(raw, 'lineName'),
    parentReleaseId: optionalString(raw, 'parentReleaseId'),
    templateSourceReleaseId: optionalString(raw, 'templateSourceReleaseId'),
    version: requireString(raw, 'version'),
    state,
    definitionGeneration: optionalNumber(raw, "definitionGeneration"),
    candidate: optionalBoolean(raw, 'candidate'),
    review: (() => {
      const review = requireRecord(raw.review, 'review');
      return {
        status: requireEnum(review, ['not_submitted', 'pending', 'approved', 'rejected'] as const, 'status'),
        contractDigest: optionalString(review, 'contractDigest'),
        submittedAt: optionalString(review, 'submittedAt'),
        reviewedBy: optionalString(review, 'reviewedBy'),
        reviewedAt: optionalString(review, 'reviewedAt'),
        comment: optionalString(review, 'comment'),
      };
    })(),
    readiness: normalizeReadiness(requireRecord(raw.readiness, 'readiness')),
    compatibility: requireEnum(raw, ['not_applicable', 'compatible', 'breaking'] as const, 'compatibility'),
    releaseNotes: optionalString(raw, 'releaseNotes'),
    riskLevel: optionalEnum(raw, ['low', 'medium', 'high', 'destructive'] as const, 'riskLevel'),
    dependencies,
    environmentConstraints: optionalObject(raw, 'environmentConstraints'),
    parameters: optionalRecords(raw, 'parameters')?.map(normalizeParameter) ?? [],
    actions,
    artifacts: optionalRecords(raw, 'artifacts')?.map(normalizeArtifact) ?? [],
    images: optionalRecords(raw, 'images')?.map(normalizeImage) ?? [],
    playbookFiles: optionalRecords(raw, 'playbookFiles')?.map(normalizeWorkspaceFile) ?? [],
    playbookFileCount: optionalNumber(raw, 'playbookFileCount') ?? 0,
    playbookTreeSha256: optionalString(raw, 'playbookTreeSha256'),
    playbookWorkspaceRoot: optionalString(raw, 'playbookWorkspaceRoot'),
    createdAt: optionalString(raw, 'createdAt'),
    releasedAt: optionalString(raw, 'releasedAt'),
    deprecatedAt: optionalString(raw, 'deprecatedAt'),
  };
}

function normalizeReadiness(raw: LooseRecord): ComponentRelease['readiness'] {
  return {
    status: requireEnum(raw, ['ready', 'blocked', 'risky'] as const, 'status'),
    blockers: requireRecords(raw, 'blockers').map((blocker) => ({
      code: requireString(blocker, 'code'),
      message: requireString(blocker, 'message'),
      actionUrl: requireString(blocker, 'actionUrl'),
    })),
    installEvidenceRunId: optionalString(raw, 'installEvidenceRunId'),
    rollbackEvidenceRunId: optionalString(raw, 'rollbackEvidenceRunId'),
    transitionEvidenceRunId: optionalString(raw, 'transitionEvidenceRunId'),

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
	const action = optionalRecord(raw, 'action');
  return {
    path: requireString(raw, 'path'),
    filename: requireString(raw, 'filename'),
    content: requireString(raw, 'content'),
    sha256: requireString(raw, 'sha256'),
    updatedAt: optionalString(raw, 'updatedAt'),
		action: action ? normalizeAction(action) : undefined,
  };
}

function normalizeWorkspaceFile(raw: LooseRecord): PlaybookWorkspaceFile {
  return {
    releaseId: requireString(raw, 'releaseId'),
    path: requireString(raw, 'path'),
    sha256: requireString(raw, 'sha256'),
    sizeBytes: requireNumber(raw, 'sizeBytes'),
    mediaType: requireString(raw, 'mediaType'),
    updatedAt: optionalString(raw, 'updatedAt'),
    editable: optionalBoolean(raw, 'editable'),
    content: optionalString(raw, 'content'),
  };
}

function normalizeWorkspace(raw: LooseRecord): PlaybookWorkspace {
  return {
    root: requireString(raw, 'root'),
    treeSha256: requireString(raw, 'treeSha256'),
    files: requireRecords(raw, 'files').map(normalizeWorkspaceFile),
    references: optionalRecord(raw, 'references') as PlaybookWorkspace['references'],
  };
}

function normalizeComponent(raw: LooseRecord): Component {
  const releases = optionalRecords(raw, 'releases')?.map((release) => normalizeRelease(release));
  const byId = new Map(releases?.map((release) => [release.id, release]));
  const releaseLines = optionalRecords(raw, 'releaseLines')?.map((line) => ({
    id: requireString(line, 'id'),
    componentId: requireString(line, 'componentId'),
    name: requireString(line, 'name'),
    environmentConstraints: optionalObject(line, 'environmentConstraints'),
    latestReleasedId: optionalString(line, 'latestReleasedId'),
    currentDraftId: optionalString(line, 'currentDraftId'),
    evolutionEligible: optionalBoolean(line, 'evolutionEligible') ?? false,
    evolutionParentId: optionalString(line, 'evolutionParentId'),
    evolutionBlockedReason: optionalString(line, 'evolutionBlockedReason'),
    releases: requireStringArray(line, 'releaseIds').map((id) => {
      const release = byId.get(id);
      if (!release || release.lineId !== line.id) throw invalidResponse(200, '发布线引用了无效的 Release。');
      return release;
    }),
    createdAt: requireString(line, 'createdAt'),
  }));
  const canonicalReleases = releases;
  const context = optionalRecord(raw, 'readContext');
  return {
    id: requireString(raw, 'id'),
    name: requireString(raw, 'name'),
    slug: optionalString(raw, 'slug'),
    description: optionalString(raw, 'description'),
    ownerId: requireString(raw, 'ownerId'),
    ownerName: optionalString(raw, 'ownerName'),
    layer: requireEnum(raw, COMPONENT_LAYERS, 'layer'),
    tags: requireStringArray(raw, 'tags'),
    latestRelease: canonicalReleases?.[0],
    releases: canonicalReleases,
    releaseLines,
    readContext: context ? {
      evidence: Object.fromEntries(Object.entries(requireRecord(context.evidence, 'evidence')).map(([id, value]) => [id, normalizeEvidenceGroup(requireRecord(value, 'release evidence'))])),
      workItems: requireRecords(context, 'workItems').map(normalizeWorkItem),
      parameterConsumers: requireRecords(context, 'parameterConsumers').map((item) => ({ componentName: requireString(item, 'componentName'), version: optionalString(item, 'version'), upstreamParameter: requireString(item, 'upstreamParameter'), targetParameter: requireString(item, 'targetParameter'), label: requireString(item, 'label') })),
    } : undefined,
    releaseCount: optionalNumber(raw, 'releaseCount'),
    updatedAt: optionalString(raw, 'updatedAt'),
  };
}

function normalizeEvidence(raw: LooseRecord): EvidenceSummary {
  return { id: requireString(raw, 'id'), status: requireEnum(raw, RUN_STATUSES, 'status'), environmentId: requireString(raw, 'environmentId'), environmentName: requireString(raw, 'environmentName'), createdAt: requireString(raw, 'createdAt'), finishedAt: optionalString(raw, 'finishedAt'), matchesContract: requireBoolean(raw, 'matchesContract') };
}
function normalizeEvidenceGroup(raw: LooseRecord): ReleaseEvidenceSummary {
  const optional = (key: string) => { const value = optionalRecord(raw, key); return value ? normalizeEvidence(value) : undefined; };
  return { currentInstall: optional('currentInstall'), currentRollback: optional('currentRollback'), currentTransition: optional('currentTransition'), historicalInstall: optional('historicalInstall'), historicalRollback: optional('historicalRollback'), historicalTransition: optional('historicalTransition'), currentById: Object.fromEntries(Object.entries(requireRecord(raw.currentById, 'currentById')).map(([id, value]) => [id, normalizeEvidence(requireRecord(value, 'evidence'))])) };
}
function normalizeRunSummary(raw: LooseRecord): RunSummary {
  return {
    archiveStatus: optionalString(raw,'archiveStatus'), archivedAt: optionalString(raw,'archivedAt'), archiveSizeBytes: optionalNumber(raw,'archiveSizeBytes'), id: requireString(raw, 'id'), kind: optionalEnum(raw, ['component_test', 'scenario_test', 'scenario_run', 'environment_rollback'] as const, 'kind'), name: requireString(raw, 'name'), status: requireEnum(raw, RUN_STATUSES, 'status'), environmentId: requireString(raw, 'environmentId'), environmentName: requireString(raw, 'environmentName'), createdAt: requireString(raw, 'createdAt'), startedAt: optionalString(raw, 'startedAt'), finishedAt: optionalString(raw, 'finishedAt'), queuePosition: optionalNumber(raw, 'queuePosition'), approvalId: optionalString(raw, 'approvalId'), action: optionalString(raw, 'action') ? optionalEnum(raw, ACTION_TYPES, 'action') : undefined, componentReleaseId: optionalString(raw, 'componentReleaseId'), componentName: optionalString(raw, 'componentName'), scenarioId: optionalString(raw, 'scenarioId'), scenarioName: optionalString(raw, 'scenarioName') };
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
      contractAvailability: optionalEnum(data, ['available', 'unshared', 'missing'] as const, 'contractAvailability'),
      componentOwnerId: optionalString(data, 'componentOwnerId'),
      componentOwnerName: optionalString(data, 'componentOwnerName'),
      label: requireString(data, 'label'),
      componentId: requireString(data, 'componentId'),
      releaseId: requireString(data, 'releaseId'),
      version: optionalString(data, 'version'),
      action: optionalEnum(data, ACTION_TYPES, 'action'),
      hostGroup: optionalString(data, 'hostGroup'),
      parameterValues: optionalObject(data, 'parameterValues'),
      dependencySources: optionalRecord(data, 'dependencySources') as Record<string, string> | undefined,
      layer: optionalEnum(data, COMPONENT_LAYERS, 'layer'),
    },
  };
}

function normalizeScenarioEdge(raw: LooseRecord): ScenarioEdge {
  const kind = optionalEnum(raw, ['dependency', 'sequence'] as const, 'kind');
  const dependencyId = optionalString(raw, 'dependencyId');
  return {
    id: requireString(raw, 'id'),
    source: requireString(raw, 'source'),
    target: requireString(raw, 'target'),
    ...(kind ? { kind } : {}),
    ...(dependencyId ? { dependencyId } : {}),
  };
}

function normalizeScenarioAcceptanceJob(raw: LooseRecord): ScenarioAcceptanceJob {
  return {legacyYamlSettings: normalizeLegacyYaml(raw, raw.runtimeChecks), id: requireString(raw, 'id'), name: requireString(raw, 'name'), purpose: requireString(raw, 'purpose'),
    hostGroup: requireString(raw, 'hostGroup'), timeoutSeconds: requireNumber(raw, 'timeoutSeconds'),
    riskLevel: requireEnum(raw, ['low', 'medium', 'high', 'destructive'] as const, 'riskLevel'),
    requiredCredentials: optionalStringArray(raw, 'requiredCredentials') ?? [], become: requireBoolean(raw, 'become'),
    playbook: requireString(raw, 'playbook'),
    playbookSha256: optionalString(raw, 'playbookSha256') ?? '', mayMutate: requireBoolean(raw, 'mayMutate') };
}
function normalizeScenarioBinding(raw: LooseRecord): ScenarioParameterBinding {
  return { parameter: requireString(raw, 'parameter'), source: requireEnum(raw, ['node', 'environment'] as const, 'source'),
    nodeId: optionalString(raw, 'nodeId'), sourceParameter: requireString(raw, 'sourceParameter') };
}
function normalizeScenarioEvidence(raw: LooseRecord | undefined): ScenarioTestEvidence | undefined {
  return raw ? { runId: optionalString(raw, 'runId'), valid: requireBoolean(raw, 'valid'), reason: optionalString(raw, 'reason'), testedAt: optionalString(raw, 'testedAt') } : undefined;
}
function normalizeScenarioAcceptance(raw: LooseRecord): ScenarioAcceptance {
  return { revisionId: requireString(raw, 'revisionId'), revisionDigest: requireString(raw, 'revisionDigest'),
    editable: requireBoolean(raw, 'editable'), jobs: requireRecords(raw, 'jobs').map(normalizeScenarioAcceptanceJob),
    parameters: requireRecords(raw, 'parameters').map(normalizeParameter), values: optionalObject(raw, 'values') ?? {},
    bindings: requireRecords(raw, 'bindings').map(normalizeScenarioBinding), workspace: normalizeWorkspace(requireRecord(raw.workspace, 'acceptance workspace')) };
}
function normalizeScenarioExecution(raw: LooseRecord): ScenarioExecutionPreview {
  return { scenarioRevisionId: requireString(raw, 'scenarioRevisionId'), environmentId: requireString(raw, 'environmentId'),
    executionMode: requireEnum(raw, ['install', 'upgrade', 'baseline_verify'] as const, 'executionMode'),
    planDigest: requireString(raw, 'planDigest'), sourceRevisionId: optionalString(raw, 'sourceRevisionId'), baselineRunId: optionalString(raw, 'baselineRunId'), ready: requireBoolean(raw, 'ready'), needsApproval: optionalBoolean(raw, 'needsApproval'),
    operations: (optionalRecords(raw, 'operations') ?? []).map(item => ({ nodeId: requireString(item, 'nodeId'), name: requireString(item, 'name'), change: requireString(item, 'change'), fromReleaseId: optionalString(item, 'fromReleaseId'), toReleaseId: optionalString(item, 'toReleaseId') })),
    steps: (optionalRecords(raw, 'steps') ?? []).map(item => ({ nodeId: optionalString(item, 'nodeId'), name: optionalString(item, 'name'), action: optionalString(item, 'action'), phase: optionalString(item, 'stage') || optionalString(item, 'phase'), kind: optionalString(item, 'sourceType'), playbook: optionalString(item, 'playbook'), hostGroup: optionalString(item, 'limit') || optionalString(item, 'hostGroup') })),
    issues: (optionalRecords(raw, 'issues') ?? []).map(item => ({ code: requireString(item, 'code'), message: requireString(item, 'message'), nodeId: optionalString(item, 'nodeId') })),
    installationTest: normalizeScenarioEvidence(optionalRecord(raw, 'installationTest')), upgradeTest: normalizeScenarioEvidence(optionalRecord(raw, 'upgradeTest')) };
}

function normalizeRevision(raw: LooseRecord): ScenarioRevision {
  return {
    environmentConstraints: optionalObject(raw, 'environmentConstraints') ?? {},
    id: requireString(raw, 'id'),
    scenarioId: requireString(raw, 'scenarioId'),
    revision: requireNumber(raw, 'revision'),
    state: requireEnum(raw, ['draft', 'testing', 'test_passed', 'released', 'deprecated', 'abandoned'] as const, 'state'),
    nodes: requireRecords(raw, 'nodes').map(normalizeScenarioNode),
    edges: requireRecords(raw, 'edges').map(normalizeScenarioEdge),
    sourceRevisionId: optionalString(raw, 'sourceRevisionId'), sourceRunId: optionalString(raw, 'sourceRunId'),
    revisionDigest: optionalString(raw, 'revisionDigest'), digestVersion: optionalNumber(raw, 'digestVersion'),
    upgradeConstraints: optionalRecords(raw, 'upgradeConstraints')?.map(normalizeScenarioEdge) ?? [],
    acceptanceJobs: optionalRecords(raw, 'acceptanceJobs')?.map(normalizeScenarioAcceptanceJob) ?? [],
    acceptanceParameters: optionalRecords(raw, 'acceptanceParameters')?.map(normalizeParameter) ?? [],
    acceptanceValues: optionalObject(raw, 'acceptanceValues') ?? {},
    acceptanceBindings: optionalRecords(raw, 'acceptanceBindings')?.map(normalizeScenarioBinding) ?? [],
    acceptanceWorkspaceRoot: optionalString(raw, 'acceptanceWorkspaceRoot'), acceptanceTreeSha256: optionalString(raw, 'acceptanceTreeSha256'),
    installationTest: normalizeScenarioEvidence(optionalRecord(raw, 'installationTest')),
    upgradeTest: normalizeScenarioEvidence(optionalRecord(raw, 'upgradeTest')),
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
    environmentConstraints: optionalObject(raw, 'environmentConstraints') ?? {},
    ownerName: optionalString(raw, 'ownerName'),
    forkedFromScenarioId: optionalString(raw, 'forkedFromScenarioId'), forkedFromRevisionId: optionalString(raw, 'forkedFromRevisionId'), forkedFromDigest: optionalString(raw, 'forkedFromDigest'),
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
    configured: optionalBoolean(raw, 'configured'),
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
    parameters: optionalRecord(raw, 'parameters') ?? {},
    variables,
    credentialRefs: requireRecords(raw, 'credentialRefs').map(normalizeCredential),
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

function normalizeEnvironmentSSHCheck(raw: LooseRecord): EnvironmentSSHCheck {
  return {
    id: requireString(raw, 'id'),
    environmentId: requireString(raw, 'environmentId'),
    environmentRevisionId: requireString(raw, 'environmentRevisionId'),
    status: requireEnum(raw, ['healthy', 'degraded'] as const, 'status'),
    durationMs: requireNumber(raw, 'durationMs'),
    results: requireRecords(raw, 'results').map((item) => ({
      kind: requireEnum(item, ['host', 'configuration'] as const, 'kind'),
      name: requireString(item, 'name'),
      address: requireString(item, 'address'),
      user: optionalString(item, 'user'),
      status: requireEnum(item, ['passed', 'unreachable', 'failed', 'skipped'] as const, 'status'),
      errorCode: optionalString(item, 'errorCode'),
      message: optionalString(item, 'message'),
    })),
    checkedAt: requireString(raw, 'checkedAt'),
  };
}

function normalizeEnvironmentConnectivityCheck(raw: LooseRecord): EnvironmentConnectivityCheck {
  return {
    tcpCheck: normalizeEnvironmentHealthCheck(requireRecord(raw.tcpCheck, 'TCP connectivity check')),
    sshCheck: normalizeEnvironmentSSHCheck(requireRecord(raw.sshCheck, 'SSH connectivity check')),
  };
}

function normalizeEnvironment(raw: LooseRecord): Environment {
  const revision = optionalRecord(raw, 'currentRevision');
  const revisions = optionalRecords(raw, 'revisions')?.map(normalizeEnvironmentRevision);
  const healthCheck = optionalRecord(raw, 'healthCheck');
  const sshCheck = optionalRecord(raw, 'sshCheck');
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
    sshCheck: sshCheck ? normalizeEnvironmentSSHCheck(sshCheck) : undefined,
    archivedAt: optionalString(raw, 'archivedAt'),
    updatedAt: optionalString(raw, 'updatedAt'),
  };
}

function normalizeEnvironmentLifecycle(raw: LooseRecord): EnvironmentLifecycle {
  return {
    revisionCount: requireNumber(raw, 'revisionCount'),
    runCount: requireNumber(raw, 'runCount'),
    activeRunCount: requireNumber(raw, 'activeRunCount'),
    imageBuildCount: requireNumber(raw, 'imageBuildCount'),
    activeImageBuildCount: requireNumber(raw, 'activeImageBuildCount'),
    installationCount: requireNumber(raw, 'installationCount'),
    archived: requireBoolean(raw, 'archived'),
    canDelete: requireBoolean(raw, 'canDelete'),
    canArchive: requireBoolean(raw, 'canArchive'),
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
    hostGroup: optionalString(raw,"limit"),
    sourceType: optionalString(raw, 'sourceType'), stage: optionalString(raw, 'stage'), acceptanceJobId: optionalString(raw, 'acceptanceJobId'), scenarioRevisionId: optionalString(raw, 'scenarioRevisionId'),
    parentAction: optionalString(raw,'parentAction'),phase: optionalString(raw,'phase'),parentActionId:optionalString(raw,'parentActionId'),actionId:optionalString(raw,'actionId'),sourceNodeId:optionalString(raw,'sourceNodeId'),role:optionalString(raw,'role'),contentDigest:optionalString(raw,'contentDigest'),
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
    decidedBy: optionalString(raw, 'decidedBy'),
    decision: optionalString(raw, 'decision'),
    reason: optionalString(raw, 'reason'),
  };
}

function normalizeDeliveryRequirement(raw: LooseRecord): DeliveryRequirement {
  return {
    id: requireString(raw, 'id'), kind: requireEnum(raw, ['artifact', 'image'] as const, 'kind'),
    name: requireString(raw, 'name'), identity: requireString(raw, 'identity'), source: requireString(raw, 'source'),
    target: optionalString(raw, 'target'), sourceReadable: requireBoolean(raw, 'sourceReadable'),
    targetPresent: requireBoolean(raw, 'targetPresent'), transferAvailable: requireBoolean(raw, 'transferAvailable'),
    componentName: requireString(raw, 'componentName'),
  };
}

function normalizeDeliveryDecision(raw: LooseRecord): DeliveryDecision {
  return {
    requirementId: requireString(raw, 'requirementId'), mode: requireEnum(raw, ['direct', 'transfer'] as const, 'mode'),
    decidedBy: optionalString(raw, 'decidedBy'), decidedAt: optionalString(raw, 'decidedAt'),
  };
}

function normalizeDeliveryResult(raw: LooseRecord): DeliveryResult {
  return {
    requirementId: requireString(raw, 'requirementId'), mode: requireEnum(raw, ['direct', 'transfer'] as const, 'mode'),
    status: requireEnum(raw, ['pending', 'direct', 'reused_target', 'transferred', 'failed'] as const, 'status'),
    actualLocation: optionalString(raw, 'actualLocation'), message: optionalString(raw, 'message'), completedAt: optionalString(raw, 'completedAt'),
  };
}

function normalizeRun(raw: LooseRecord): Run {
  const source = raw;
  const counts = optionalRecord(source,'purposeCounts');
  const purposeCounts = counts ? {components:requireNumber(counts,'components'),finalVerification:requireNumber(counts,'finalVerification'),acceptance:requireNumber(counts,'acceptance'),total:requireNumber(counts,'total')} : undefined;
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
    alias: requireString(item, 'alias'), sourceUrl: requireString(item, 'sourceUrl'), targetStation: requireString(item, 'targetStation'),
    relativePath: requireString(item, 'relativePath'), sha256: requireString(item, 'sha256'),
  }));
  const imageTransfers = optionalRecords(source, 'imageTransfers')?.map((item) => ({
    sourceRegistry: requireString(item, 'sourceRegistry'), targetRegistry: requireString(item, 'targetRegistry'),
    sourceDigest: requireString(item, 'sourceDigest'), targetDigest: requireString(item, 'targetDigest'),
  }));
  const deliveryRequirements = optionalRecords(source, 'deliveryRequirements')?.map(normalizeDeliveryRequirement);
  const deliveryDecisions = optionalRecords(source, 'deliveryDecisions')?.map(normalizeDeliveryDecision);
  const deliveryResults = optionalRecords(source, 'deliveryResults')?.map(normalizeDeliveryResult);
  return {
    purposeCounts,
    executionMode: optionalEnum(source, ['install', 'upgrade', 'baseline_verify'] as const, 'executionMode'), sourceRevisionId: optionalString(source, 'sourceRevisionId'), baselineRunId: optionalString(source, 'baselineRunId'), jobDigest: optionalString(source,'jobDigest'), exitCode: typeof source.exitCode === 'number' ? source.exitCode : undefined,
    archive: optionalObject(raw,'archive') as unknown as ArchiveInfo | undefined,
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
    retryOfRunId: optionalString(source, 'retryOfRunId'),
    retryRootRunId: optionalString(source, 'retryRootRunId'),
    retryAttempt: optionalNumber(source, 'retryAttempt'),
    retryStartStep: optionalNumber(source, 'retryStartStep'),
    queuePosition: optionalNumber(source, 'queuePosition'),
    progress: optionalNumber(source, 'progress'),
    steps,
    approval: approval ? normalizeApproval(approval) : undefined,
    resolvedParametersByNode: optionalRecord(source, 'resolvedParametersByNode') as Run['resolvedParametersByNode'],
    backups,
    artifactTransfers,
    imageTransfers,
    deliveryRequirements,
    deliveryDecisions,
    deliveryResults,
    createdAt: optionalString(source, 'createdAt'),
    startedAt: optionalString(source, 'startedAt'),
    finishedAt: optionalString(source, 'finishedAt'),
  };
}

function normalizeComponentTestPlan(raw: LooseRecord): ComponentTestPlan {
  const source = raw;
  const steps = optionalRecords(source, 'steps')?.map((step) => ({
    name: optionalString(step,'name'),sourceType: optionalString(step,'sourceType'),stage:optionalString(step,'stage'),
    order: requireNumber(step, 'order'),
    rollbackSourceActionId:optionalString(step,'rollbackSourceActionId'),phase:optionalString(step,'phase'),parentActionId:optionalString(step,'parentActionId'),actionId:optionalString(step,'actionId'),nodeId:optionalString(step,'nodeId'),
    componentId: requireString(step, 'componentId'),
    componentName: requireString(step, 'componentName'),
    releaseId: requireString(step, 'releaseId'),
    releaseVersion: requireString(step, 'releaseVersion'),
    action: requireEnum(step, [...ACTION_TYPES, 'acceptance'] as const, 'action'),
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
    deliveryRequirements: requireRecords(source, 'deliveryRequirements').map(normalizeDeliveryRequirement),
  };
}

function normalizeEnvironmentRollbackPlan(raw: LooseRecord): EnvironmentRollbackPlan {
  return {
    ...normalizeComponentTestPlan(raw),
    nodes: optionalStringArray(raw, 'nodes') ?? [],
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

function normalizeEnvironmentImportPlan(raw: LooseRecord): EnvironmentImportPlan {
  return {
    planDigest: requireString(raw, 'planDigest'),
    targetKind: requireEnum(raw, ['new', 'existing'] as const, 'targetKind'),
    targetEnvironmentId: optionalString(raw, 'targetEnvironmentId'),
    targetCurrentRevisionId: optionalString(raw, 'targetCurrentRevisionId'),
    nextRevision: requireNumber(raw, 'nextRevision'),
    hostCount: requireNumber(raw, 'hostCount'),
    variableCount: requireNumber(raw, 'variableCount'),
    parameterCount: requireNumber(raw, 'parameterCount'),
    credentialRefCount: requireNumber(raw, 'credentialRefCount'),
    changes: requireStringArray(raw, 'changes'),
    warnings: requireStringArray(raw, 'warnings'),
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
    role: requireEnum(raw, ['component_owner', 'scenario_owner', 'environment_owner', 'platform_admin'] as const, 'role'),
  };
}

function normalizeWorkbench(value: unknown): Workbench {
  const raw = requireRecord(value, 'workbench');
  const summary = requireRecord(field(raw, 'summary'), 'workbench summary');
  const assets = requireRecord(field(raw, 'assets'), 'workbench assets');
  return {
    generatedAt: requireString(raw, 'generatedAt'),
    role: requireEnum(raw, ['component_owner', 'scenario_owner', 'environment_owner', 'platform_admin'] as const, 'role'),
    summary: {
      critical: requireNumber(summary, 'critical'),
      actionRequired: requireNumber(summary, 'actionRequired'),
      inProgress: requireNumber(summary, 'inProgress'),
      informational: requireNumber(summary, 'informational'),
    },
    assets: {
      components: requireNumber(assets, 'components'),
      scenarios: requireNumber(assets, 'scenarios'),
      environments: requireNumber(assets, 'environments'),
    },
    items: requireRecords(raw, 'items').map(normalizeWorkItem),
  };
}

function normalizeWorkItem(item: LooseRecord): Workbench['items'][number] {
      const subject = requireRecord(field(item, 'subject'), 'work item subject');
      return {
        id: requireString(item, 'id'),
        kind: requireEnum(item, ['component_draft', 'component_review', 'scenario_revision', 'environment', 'run', 'upstream_impact', 'catalog_backup'] as const, 'kind'),
        priority: requireEnum(item, ['critical', 'high', 'normal', 'info'] as const, 'priority'),
        status: requireEnum(item, ['blocked', 'action_required', 'in_progress', 'attention'] as const, 'status'),
        title: requireString(item, 'title'),
        subject: {
          type: requireString(subject, 'type'), id: requireString(subject, 'id'), parentId: optionalString(subject, 'parentId'),
          name: requireString(subject, 'name'), version: optionalString(subject, 'version'), revision: optionalNumber(subject, 'revision'), environment: optionalString(subject, 'environment'),
        },
        reasons: requireRecords(item, 'reasons').map(normalizeWorkReason),
        primaryAction: normalizeWorkAction(field(item, 'primaryAction')),
        secondaryActions: requireRecords(item, 'secondaryActions').map(normalizeWorkAction),
        updatedAt: requireString(item, 'updatedAt'),
      };
}

function normalizeWorkAction(value: unknown): WorkAction {
  const action = requireRecord(value, 'work action');
  return { label: requireString(action, 'label'), href: requireString(action, 'href') };
}

function normalizeWorkReason(reason: LooseRecord): WorkReason {
  const causeValue = field(reason, 'cause');
  const cause = causeValue === undefined || causeValue === null ? undefined : requireRecord(causeValue, 'work cause');
  const actionValue = field(reason, 'nextAction');
  return {
    code: requireString(reason, 'code'),
    message: requireString(reason, 'message'),
    evidenceRunId: optionalString(reason, 'evidenceRunId'),
    cause: cause ? {
      kind: requireString(cause, 'kind'), summary: requireString(cause, 'summary'), actorId: optionalString(cause, 'actorId'),
      actorName: optionalString(cause, 'actorName'), action: optionalString(cause, 'action'), at: optionalString(cause, 'at'),
    } : undefined,
    nextAction: actionValue === undefined || actionValue === null ? undefined : normalizeWorkAction(actionValue),
  };
}

function normalizeWorkExplanation(value: unknown): WorkExplanation {
  const explanation = requireRecord(value, 'work explanation');
  const primary = field(explanation, 'primaryAction');
  return {
    reasons: requireRecords(explanation, 'reasons').map(normalizeWorkReason),
    primaryAction: primary === undefined || primary === null ? undefined : normalizeWorkAction(primary),
    secondaryActions: requireRecords(explanation, 'secondaryActions').map(normalizeWorkAction),
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
    resourceContract: action.resourceContract,
    preCheckActionId: action.preCheckActionId ?? '',
    postCheckActionId: action.postCheckActionId ?? '',
    become: action.become ?? false,
    tags: action.tags,
    hostGroup: action.hostGroup,
    timeoutSeconds: action.timeoutSeconds,
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
    ...(dependency.kind ? { kind: dependency.kind } : {}),
    upstreamComponentId: dependency.componentId,
    upstreamReleaseId: dependency.releaseId,
    purpose: dependency.purpose,
    parameterMappings: dependency.parameterMappings ?? [],
  };
}

function serializeRelease(input: Partial<ComponentRelease>) {
  return {
    expectedDefinitionGeneration: input.definitionGeneration,
    version: input.version,
    status: input.state,
    releaseNotes: input.releaseNotes,
    compatibility: input.compatibility,
    riskLevel: input.riskLevel,
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

function normalizeCatalogRepository(value: unknown): CatalogRepositoryStatus {
  const raw = requireRecord(value, 'Catalog repository');
  return {
    enabled: requireBoolean(raw, 'enabled'), configured: requireBoolean(raw, 'configured'),
    reasonCode: optionalString(raw, 'reasonCode'), reason: optionalString(raw, 'reason'), path: optionalString(raw, 'path'),
    branch: requireString(raw, 'branch'), allowedRoot: requireString(raw, 'allowedRoot'),
    recoveryPoints: (optionalRecords(raw, 'recoveryPoints') ?? []).map((item) => ({ ref: requireString(item, 'ref'), commit: requireString(item, 'commit'), createdAt: requireString(item, 'createdAt') })),
    restoreTargetKnown: optionalBoolean(raw, 'restoreTargetKnown') ?? false,
    targetCatalogEmpty: optionalBoolean(raw, 'targetCatalogEmpty') ?? false,
    targetComponentCount: optionalNumber(raw, 'targetComponentCount') ?? 0,
    targetScenarioCount: optionalNumber(raw, 'targetScenarioCount') ?? 0,
    behind: optionalBoolean(raw, 'behind') ?? false,
    currentGeneration: optionalNumber(raw, 'currentGeneration') ?? 0,
    backedUpGeneration: optionalNumber(raw, 'backedUpGeneration') ?? 0,
    lastSuccessfulAt: optionalString(raw, 'lastSuccessfulAt'), lastError: optionalString(raw, 'lastError'), lastErrorAt: optionalString(raw, 'lastErrorAt'),
  };
}

function normalizeCatalogBackupResult(value: unknown): CatalogBackupResult {
  const raw = requireRecord(value, 'Catalog backup result');
  const status = requireString(raw, 'status');
  if (status !== 'success') throw invalidResponse(200, '平台 API 的备份状态必须是 success。');
  return {
    backupId: requireString(raw, 'backupId'), status, reason: requireString(raw, 'reason'),
    createdAt: requireString(raw, 'createdAt'), completedAt: requireString(raw, 'completedAt'),
    publicationGeneration: requireNumber(raw, 'publicationGeneration'),
    gitCommit: requireString(raw, 'gitCommit'), gitTag: requireString(raw, 'gitTag'),
  };
}

function normalizeCatalogRestorePlan(value: unknown): CatalogRestorePlan {
  const raw = requireRecord(value, 'Catalog restore plan');
  const counts = requireRecord(raw.counts, 'counts');
  const normalizedCounts: Record<string, number> = {};
  for (const [name, count] of Object.entries(counts)) {
    if (typeof count !== 'number' || !Number.isFinite(count)) throw invalidResponse(200, `平台 API 的 counts.${name} 必须是数字。`);
    normalizedCounts[name] = count;
  }
  return {
    gitCommit: requireString(raw, 'gitCommit'), schemaContract: requireString(raw, 'schemaContract'),
    catalogSha256: requireString(raw, 'catalogSha256'), counts: normalizedCounts,
    playbookCount: requireNumber(raw, 'playbookCount'), targetComponentCount: optionalNumber(raw, 'targetComponentCount') ?? 0,
    targetScenarioCount: optionalNumber(raw, 'targetScenarioCount') ?? 0, planDigest: requireString(raw, 'planDigest'),
  };
}

function normalizePreparation(value: unknown): import('../types/executionPreparation').PreparationSession {
 const raw=requireRecord(value,'execution preparation');const input=requireRecord(raw.input,'preparation request');const output=requireRecord(raw.output,'preparation result');
 const kind=requireEnum(input,['component_test','scenario_execution'] as const,'kind');
 return {id:requireString(raw,'id'),status:requireEnum(raw,['queued','running','succeeded','failed','cancelled','interrupted','timed_out'] as const,'status'),input:{kind,subjectId:requireString(input,'subjectId'),environmentId:requireString(input,'environmentId')},output:{checks:requireRecords(output,'checks').map(check=>({id:requireString(check,'id'),category:requireString(check,'category'),label:requireString(check,'label'),status:requireString(check,'status'),host:optionalString(check,'host'),source:optionalString(check,'source'),message:optionalString(check,'message'),elapsedMs:requireNumber(check,'elapsedMs'),startedAt:optionalString(check,'startedAt')})),plan:output.plan,error:optionalString(output,'error'),explanation:output.explanation?normalizeWorkExplanation(output.explanation):undefined}};
}

export const api = {
  async runDiagnostics(id: string, signal?: AbortSignal): Promise<import('../types/domain').RunDiagnostics> {
    const raw = requireRecord(unwrap(await get<unknown>(`/runs/${encodeURIComponent(id)}/diagnostics`, signal)), 'diagnostics');
    return {
      runId: requireString(raw, 'runId'), status: requireString(raw, 'status') as Run['status'],
      capturedAt: requireString(raw, 'capturedAt'), lastLogId: requireNumber(raw, 'lastLogId'), logCount: requireNumber(raw, 'logCount'), omitted: optionalNumber(raw, 'omitted'),
      items: requireRecords(raw, 'items').map(item => ({
        message: requireString(item, 'message'), source: requireString(item, 'source') as import('../types/domain').RunDiagnostic['source'],
        stepId: optionalString(item, 'stepId'), logId: optionalNumber(item, 'logId'), component: optionalString(item, 'component'), phase: optionalString(item, 'phase'),
        task: optionalString(item, 'task'), host: optionalString(item, 'host'), exitCode: optionalNumber(item, 'exitCode'), stdout: optionalString(item, 'stdout'), stderr: optionalString(item, 'stderr'), raw: optionalString(item, 'raw'), truncated: optionalBoolean(item, 'truncated'),
      })),
    };
  },
  async downloadRunLogs(id: string, signal?: AbortSignal): Promise<Blob> {
    const response = await fetch(`${API_ROOT}/runs/${encodeURIComponent(id)}/log-bundle`, { signal, credentials: 'include', cache: 'no-store', headers: { Accept: 'application/gzip' } });
    if (!response.ok) {
      const body = parseJSON(await response.text());
      throw new ApiError(response.status, isApiErrorBody(body) ? body : {});
    }
    if (!response.headers.get('Content-Type')?.toLowerCase().includes('application/gzip')) throw invalidResponse(response.status, '日志下载返回了无效的文件类型，请重试。');
    return response.blob();
  },
 async verifiedJobEligibility(id: string, signal?: AbortSignal): Promise<{eligible:boolean; reason?:string;rootRunId?:string;stageCount:number;evidenceRunIds:string[]}>{const raw=requireRecord(unwrap(await get<unknown>(`/runs/${encodeURIComponent(id)}/verified-job-eligibility`,signal)),'job eligibility');return {eligible:requireBoolean(raw,'eligible'),reason:optionalString(raw,'reason'),rootRunId:optionalString(raw,'rootRunId'),stageCount:requireNumber(raw,'stageCount'),evidenceRunIds:requireStringArray(raw,'evidenceRunIds')};},
 async createPreparation(input: import('../types/executionPreparation').PreparationRequest & { idempotencyKey: string }): Promise<import('../types/executionPreparation').PreparationSession> { return normalizePreparation(unwrap(await post<unknown>('/execution-preparations', input))); },
 async preparation(id: string): Promise<import('../types/executionPreparation').PreparationSession> { return normalizePreparation(unwrap(await get<unknown>(`/execution-preparations/${encodeURIComponent(id)}`))); },
 async cancelPreparation(id: string) { return unwrap(await post<unknown>(`/execution-preparations/${encodeURIComponent(id)}/cancel`, {})); },
 preparationPlan(kind: string, value: unknown): ComponentTestPlan | ScenarioExecutionPreview { const raw = requireRecord(value, 'prepared plan'); return kind === 'component_test' ? normalizeComponentTestPlan(raw) : normalizeScenarioExecution(raw); },
 async executorHealth() { return unwrap(await get<unknown>('/executor-health')) as {status:string;controller:string;ansible:string;python:string;checkedAt:string;message:string;repairLocation:string}; },
 async archiveHealth(signal?: AbortSignal): Promise<ArchiveHealth> { return unwrap(await get<unknown>('/run-retention',signal)) as ArchiveHealth; },
 async saveRetention(policy: RetentionPolicy) { return put('/run-retention',policy); },
 async archiveRuns(runIds: string[]): Promise<ArchiveInfo[]> { return unwrap(await post<unknown>('/runs/archive',{runIds})) as ArchiveInfo[]; },
 async cleanupPreview(runIds: string[]): Promise<CleanupItem[]> { return unwrap(await post<unknown>('/runs/cleanup-preview',{runIds})) as CleanupItem[]; },
 async cleanupRuns(runIds: string[]) { return post('/runs/cleanup',{runIds}); },
 archiveDownloadURL(id:string) {return `/api/v1/runs/${encodeURIComponent(id)}/archive-download`;},

  async componentUsage(id: string, releaseId: string, includeHistory: boolean, signal?: AbortSignal): Promise<ComponentUsage> { const raw=requireRecord(unwrap(await get<unknown>(`/components/${id}/usage?${new URLSearchParams({releaseId, includeHistory: String(includeHistory)})}`, signal)),"component usage");requireRecords(raw,"components");requireRecords(raw,"scenarios");requireNumber(raw,"componentCount");requireNumber(raw,"scenarioCount");return raw as unknown as ComponentUsage; },
  async sessionUsers() {
    return unwrapList(await get<unknown>('/session/users')).map(normalizeUser);
  },
  async me() {
    return normalizeUser(unwrap(await get<unknown>('/session/me')));
  },
  async switchUser(userId: string) {
    return normalizeUser(unwrap(await post<unknown>('/session/switch', { userId })));
  },
  async platformOptionCategories(signal?: AbortSignal): Promise<PlatformOptionCategory[]> {
    return unwrapList(await get<unknown>('/platform-option-categories', signal)).map((item) => item as PlatformOptionCategory);
  },
  async createPlatformOptionCategory(input: { label: string; parentCategoryId?: string; environmentRequired?: boolean }): Promise<PlatformOptionCategory> {
    return unwrap(await post<unknown>('/platform-option-categories', input)) as PlatformOptionCategory;
  },
  async renamePlatformOptionCategory(id: string, label: string): Promise<PlatformOptionCategory> {
    return unwrap(await patch<unknown>(`/platform-option-categories/${id}`, { label })) as PlatformOptionCategory;
  },
  async setPlatformOptionCategoryRetired(id: string, retired: boolean): Promise<PlatformOptionCategory> {
    return unwrap(await patch<unknown>(`/platform-option-categories/${id}`, { retired })) as PlatformOptionCategory;
  },
  async deletePlatformOptionCategory(id: string) {
    await request<unknown>(`/platform-option-categories/${id}`, { method: 'DELETE' });
  },
  async createPlatformOption(categoryId: string, label: string, parentOptionId?: string): Promise<PlatformOption> {
    return unwrap(await post<unknown>(`/platform-option-categories/${categoryId}/options`, { label, parentOptionId })) as PlatformOption;
  },
  async renamePlatformOption(id: string, label: string): Promise<PlatformOption> {
    return unwrap(await patch<unknown>(`/platform-options/${id}`, { label })) as PlatformOption;
  },
  async setPlatformOptionRetired(id: string, retired: boolean): Promise<PlatformOption> {
    return unwrap(await patch<unknown>(`/platform-options/${id}`, { retired })) as PlatformOption;
  },
  async deletePlatformOption(id: string) {
    await request<unknown>(`/platform-options/${id}`, { method: 'DELETE' });
  },
  async environmentVariableDefinitions(signal?: AbortSignal): Promise<EnvironmentVariableDefinition[]> {
    return unwrapList(await get<unknown>('/environment-variable-definitions', signal)) as EnvironmentVariableDefinition[];
  },
  async createEnvironmentVariableDefinition(input: Pick<EnvironmentVariableDefinition, 'name' | 'label' | 'description'>): Promise<EnvironmentVariableDefinition> {
    return unwrap(await post<unknown>('/environment-variable-definitions', input)) as EnvironmentVariableDefinition;
  },
  async deleteEnvironmentVariableDefinition(id: string) {
    await request<unknown>(`/environment-variable-definitions/${id}`, { method: 'DELETE' });
  },
  async workbench(signal?: AbortSignal) {
    return normalizeWorkbench(unwrap(await get<unknown>('/workbench', signal)));
  },
  async components(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/components?view=contracts', signal)).map((item) => normalizeComponent(requireRecord(item, 'component')));
  },
  async componentSummaries(signal?: AbortSignal): Promise<ComponentSummary[]> {
    return unwrapList(await get<unknown>('/components', signal)).map((value) => {
      const raw = requireRecord(value, 'component summary');
      return { id: requireString(raw, 'id'), name: requireString(raw, 'name'), slug: optionalString(raw, 'slug'), description: optionalString(raw, 'description'), ownerId: requireString(raw, 'ownerId'), ownerName: requireString(raw, 'ownerName'), layer: requireEnum(raw, COMPONENT_LAYERS, 'layer'), tags: requireStringArray(raw, 'tags'), releaseCount: requireNumber(raw, 'releaseCount'), defaultReleaseId: optionalString(raw, 'defaultReleaseId'), hasDraft: requireBoolean(raw, 'hasDraft'), needsAttention: requireBoolean(raw, 'needsAttention') };
    });
  },
  async component(id: string, signal?: AbortSignal) {
    return normalizeComponent(requireRecord(unwrap(await get<unknown>(`/components/${id}`, signal)), 'component'));
  },
  async createComponent(input: Partial<Component>) {
    return normalizeComponent(requireRecord(normalizeOptionalData(await post<unknown>('/components', input)), 'component'));
  },
  async updateComponent(id: string, input: Partial<Component>) {
    return normalizeComponent(requireRecord(normalizeOptionalData(await patch<unknown>(`/components/${id}`, input)), 'component'));
  },
  async previewReleaseDraft(componentId: string, input: { mode: 'new_line' | 'evolution'; lineName?: string; parentReleaseId?: string; templateSourceReleaseId?: string; version: string; releaseNotes: string; compatibility?: ComponentRelease['compatibility']; riskLevel?: ComponentRelease['riskLevel']; environmentConstraints?: Record<string, unknown> }) {
    return unwrap(await post<unknown>(`/components/${componentId}/release-draft-plan`, input)) as { mode: 'new_line' | 'evolution'; lineId?: string; lineName: string; parentReleaseId?: string; parentVersion?: string; templateSourceReleaseId?: string; templateSourceVersion?: string; targetVersion: string; compatibility: ComponentRelease['compatibility']; planDigest: string; actions: string[]; removedActions: string[]; playbooks: string[]; artifactCount: number; imageCount: number };
  },
  async createReleaseDraft(componentId: string, input: { mode: 'new_line' | 'evolution'; lineName?: string; parentReleaseId?: string; templateSourceReleaseId?: string; version: string; releaseNotes: string; compatibility?: ComponentRelease['compatibility']; riskLevel?: ComponentRelease['riskLevel']; environmentConstraints?: Record<string, unknown>; expectedPlanDigest: string }) {
    return normalizeReleaseActionResponse(await post<unknown>(`/components/${componentId}/release-drafts`, input));
  },
  async previewComponentImport(entries: unknown[]) {
    return unwrap(await post<unknown>('/component-imports/plan', { entries })) as { planDigest: string; order: string[]; items: Array<{ slug: string; name: string; version: string; dependencyCount: number; actionCount: number; playbookCount: number }> };
  },
  async importComponents(entries: unknown[], expectedPlanDigest: string) {
    return unwrap(await post<unknown>('/component-imports', { entries, expectedPlanDigest })) as { completedComponents: string[]; completedReleases: string[]; savedPlaybooks: string[]; createdDrafts: Record<string, string>; fileMappings: Array<{originalPath:string;currentPath:string;actionId?:string;releaseId?:string}> };
  },
  async updateRelease(releaseId: string, input: Partial<ComponentRelease>) {
    return normalizeReleaseActionResponse(await put<unknown>(`/component-releases/${releaseId}`, serializeRelease(input)));
  },
  async patchReleaseContract(releaseId: string, input: { section: 'parameters' | 'dependencies'; expectedDefinitionGeneration: number; parameters?: ComponentRelease['parameters']; dependencies?: ComponentRelease['dependencies']; newParameters?: ComponentRelease['parameters']; removeParameters?: string[] }) {
    return normalizeReleaseActionResponse(await patch<unknown>(`/component-releases/${releaseId}/contract`, {...input, dependencies: input.dependencies?.map(serializeDependency)}));
  },
  async environmentCredentialSources(environmentId: string, revisionId: string, signal?: AbortSignal): Promise<Array<{ name:string; kind:string; componentId?:string; componentName?:string; releaseId?:string; version?:string; lineName?:string; actionId?:string; actionName?:string; scenarioId?:string; scenarioName?:string; revisionId?:string; revision?:number; description?:string }>> {
    return unwrapList(await get<unknown>(`/environments/${environmentId}/credential-sources?${new URLSearchParams({revisionId})}`, signal)).map(value => {
      const raw = requireRecord(value, 'credential declaration source');
      return {name:requireString(raw,'name'),kind:requireString(raw,'kind'),componentId:optionalString(raw,'componentId'),componentName:optionalString(raw,'componentName'),releaseId:optionalString(raw,'releaseId'),version:optionalString(raw,'version'),lineName:optionalString(raw,'lineName'),actionId:optionalString(raw,'actionId'),actionName:optionalString(raw,'actionName'),scenarioId:optionalString(raw,'scenarioId'),scenarioName:optionalString(raw,'scenarioName'),revisionId:optionalString(raw,'revisionId'),revision:optionalNumber(raw,'revision'),description:optionalString(raw,'description')};
    });
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
  async registerArtifact(releaseId: string, input: { alias: string; filename: string; sourceUrl: string; sha256: string }) {
    return normalizeArtifact(requireRecord(unwrap(await post<unknown>(`/component-releases/${releaseId}/artifacts/register`, input)), 'artifact'));
  },
  async updateArtifactSource(releaseId: string, alias: string, sourceUrl: string) {
    return normalizeArtifact(requireRecord(unwrap(await patch<unknown>(`/component-releases/${releaseId}/artifacts/${encodeURIComponent(alias)}/source`, { sourceUrl })), 'artifact'));
  },
  async deleteArtifact(releaseId: string, alias: string) {
    await request<unknown>(`/component-releases/${releaseId}/artifacts/${encodeURIComponent(alias)}`, { method: 'DELETE' });
  },
  async registerImage(releaseId: string, input: { logicalName: string; sourceRef: string; digest?: string }) {
    return normalizeImage(requireRecord(unwrap(await post<unknown>(`/component-releases/${releaseId}/images/register`, input)), 'image'));
  },
  async updateImageSource(releaseId: string, logicalName: string, sourceRef: string) {
    return normalizeImage(requireRecord(unwrap(await patch<unknown>(`/component-releases/${releaseId}/images/${encodeURIComponent(logicalName)}/source`, { sourceRef })), 'image'));
  },
  async deleteImage(releaseId: string, logicalName: string) {
    await request<unknown>(`/component-releases/${releaseId}/images/${encodeURIComponent(logicalName)}`, { method: 'DELETE' });
  },
  async playbook(releaseId: string, actionId: string, signal?: AbortSignal) {
    return normalizePlaybook(requireRecord(unwrap(await get<unknown>(`/component-releases/${releaseId}/playbook?actionId=${encodeURIComponent(actionId)}`, signal)), 'Playbook'));
  },
  async savePlaybook(releaseId: string, action: ActionDefinition, content: string, expectedSha256: string, expectedTreeSha256: string, confirmYamlMigration = false) {
    return normalizePlaybook(requireRecord(unwrap(await put<unknown>(`/component-releases/${releaseId}/playbook`, { actionKind: action.type, action: serializeAction(action), content, expectedSha256, expectedTreeSha256, confirmYamlMigration })), 'Playbook'));
  },
  async uploadPlaybook(releaseId: string, action: ActionDefinition, file: File, expectedSha256: string, expectedTreeSha256: string, confirmYamlMigration = false) {
    const form = new FormData();
    form.set('playbook', file, file.name);
    form.set('actionKind', action.type);
    form.set('confirmYamlMigration', String(confirmYamlMigration));
    form.set('action', JSON.stringify(serializeAction(action)));
    form.set('expectedSha256', expectedSha256);
    form.set('expectedTreeSha256', expectedTreeSha256);
    return normalizePlaybook(requireRecord(unwrap(await postForm<unknown>(`/component-releases/${releaseId}/playbook`, form)), 'Playbook'));
  },
  async deleteActionPlaybook(releaseId: string, actionId: string, expectedSha256: string, expectedTreeSha256: string) {
    return normalizeWorkspace(requireRecord(unwrap(await request<unknown>(`/component-releases/${releaseId}/playbook?actionId=${encodeURIComponent(actionId)}&expectedSha256=${encodeURIComponent(expectedSha256)}&expectedTreeSha256=${encodeURIComponent(expectedTreeSha256)}`, { method: 'DELETE' })), 'Playbook workspace'));
  },
  async playbookWorkspace(releaseId: string) {
    return normalizeWorkspace(requireRecord(unwrap(await get<unknown>(`/component-releases/${releaseId}/playbook-workspace`)), 'Playbook workspace'));
  },
  async workspaceFile(releaseId: string, path: string) {
    return normalizeWorkspaceFile(requireRecord(unwrap(await get<unknown>(`/component-releases/${releaseId}/playbook-workspace/file?path=${encodeURIComponent(path)}`)), 'Playbook workspace file'));
  },
  async saveWorkspaceFile(releaseId: string, path: string, content: string, expectedSha256: string, expectedTreeSha256?: string) {
    return normalizeWorkspaceFile(requireRecord(unwrap(await put<unknown>(`/component-releases/${releaseId}/playbook-workspace/file`, { path, content, expectedSha256, expectedTreeSha256 })), 'Playbook workspace file'));
  },
  async uploadWorkspaceFile(releaseId: string, path: string, file: File, expectedSha256: string, expectedTreeSha256?: string) {
    const form = new FormData();
    form.set('path', path);
    form.set('file', file, file.name);
    form.set('expectedSha256', expectedSha256);
	if (expectedTreeSha256 !== undefined) form.set('expectedTreeSha256', expectedTreeSha256);
    return normalizeWorkspaceFile(requireRecord(unwrap(await postForm<unknown>(`/component-releases/${releaseId}/playbook-workspace/file`, form)), 'Playbook workspace file'));
  },
  async renameWorkspaceFile(releaseId: string, from: string, to: string, expectedSha256: string, expectedTreeSha256?: string) {
    return normalizeWorkspace(requireRecord(unwrap(await patch<unknown>(`/component-releases/${releaseId}/playbook-workspace/file`, { from, to, expectedSha256, expectedTreeSha256 })), 'Playbook workspace'));
  },
  async deleteWorkspaceFile(releaseId: string, path: string, expectedSha256: string, expectedTreeSha256?: string) {
    const tree = expectedTreeSha256 === undefined ? '' : `&expectedTreeSha256=${encodeURIComponent(expectedTreeSha256)}`;
    return normalizeWorkspace(requireRecord(unwrap(await request<unknown>(`/component-releases/${releaseId}/playbook-workspace/file?path=${encodeURIComponent(path)}&expectedSha256=${encodeURIComponent(expectedSha256)}${tree}`, { method: 'DELETE' })), 'Playbook workspace'));
  },
  workspaceFileDownloadURL(releaseId: string, path: string) {
    return `${API_ROOT}/component-releases/${releaseId}/playbook-workspace/file?path=${encodeURIComponent(path)}&download=1`;
  },
  async releaseImpact(releaseId: string, operation: 'publish' | 'deprecate' = 'deprecate'): Promise<ImpactPreview> {
    const raw = unwrap(await get<unknown>(`/component-releases/${releaseId}/impact?operation=${operation}`));
    if (!isRecord(raw)) throw invalidResponse(200, '平台 API 返回了无效的影响分析响应。');
    return {
      changeKind: raw.changeKind === 'evolution' ? 'evolution' : raw.changeKind === 'deprecation' ? 'deprecation' : 'new_line',
      lineId: typeof raw.lineId === 'string' ? raw.lineId : undefined,
      lineName: typeof raw.lineName === 'string' ? raw.lineName : undefined,
      fromReleaseId: typeof raw.fromReleaseId === 'string' ? raw.fromReleaseId : undefined,
      toReleaseId: typeof raw.toReleaseId === 'string' ? raw.toReleaseId : undefined,
      componentOwners: narrowPeople(raw.componentOwners),
      scenarioOwners: narrowPeople(raw.scenarioOwners),
      scenarios: narrowPeople(raw.scenarios),
      paths: narrowStringPaths(raw.paths),
      scenarioRunCount: typeof raw.scenarioRunCount === 'number' && Number.isInteger(raw.scenarioRunCount) && raw.scenarioRunCount >= 0 ? raw.scenarioRunCount : 0,
    };
  },
  async publishRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/publish`));
  },
  async setReleaseCandidate(releaseId: string, candidate: boolean) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/candidate`, { candidate }));
  },
  async submitReleaseReview(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/review-submission`));
  },
  async previewReleaseReview(releaseId: string, signal?: AbortSignal): Promise<ReleaseReviewPreview> {
    const raw = requireRecord(unwrap(await get<unknown>(`/component-releases/${releaseId}/review-preview`, signal)), 'release review preview');
    return {
      componentId: requireString(raw, 'componentId'), componentName: requireString(raw, 'componentName'),
      ownerId: requireString(raw, 'ownerId'), ownerName: requireString(raw, 'ownerName'),
      release: normalizeRelease(requireRecord(field(raw, 'release'), 'release')),
      playbooks: requireRecords(raw, 'playbooks').map((playbook) => ({
        actionId: requireString(playbook, 'actionId'), actionName: requireString(playbook, 'actionName'),
        actionKind: requireEnum(playbook, ACTION_TYPES, 'actionKind'), path: requireString(playbook, 'path'),
        filename: requireString(playbook, 'filename'), content: requireString(playbook, 'content'), sha256: requireString(playbook, 'sha256'),
      })),
      previewDigest: requireString(raw, 'previewDigest'),
    };
  },
  async decideReleaseReview(releaseId: string, decision: 'approve' | 'reject', comment: string, expectedPreviewDigest: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/review-decision`, { decision, comment, expectedPreviewDigest }));
  },
  async deprecateRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/deprecate`));
  },
  async restoreRelease(releaseId: string) {
    return normalizeReleaseActionResponse(await post<unknown>(`/component-releases/${releaseId}/restore`));
  },
  async deleteRelease(releaseId: string) {
    await request<unknown>(`/component-releases/${releaseId}`, { method: 'DELETE' });
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
  async releaseRunEvidence(releaseId: string, signal?: AbortSignal) {
    return unwrapList(await get<unknown>(`/component-releases/${releaseId}/run-evidence`, signal)).map((item) => normalizeRunSummary(requireRecord(item, 'run evidence')));
  },
  async previewScenarioFork(input: ScenarioForkInput): Promise<ScenarioForkPlan> {
    return requireRecord(unwrap(await post<unknown>('/scenarios/fork-plan', input)), 'scenario fork plan') as unknown as ScenarioForkPlan;
  },
  async forkScenario(input: ScenarioForkInput) {
    return normalizeScenario(requireRecord(unwrap(await post<unknown>('/scenarios/forks', input)), 'scenario'));
  },
  async reopenScenarioRevision(revisionId: string, expectedRevisionDigest: string) {
    return normalizeRevision(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/edit`, { expectedRevisionDigest })), 'scenario revision'));
  },
  async saveScenarioUpgradeConstraints(revisionId: string, edges: ScenarioEdge[], expectedRevisionDigest: string) {
    return normalizeRevision(requireRecord(unwrap(await put<unknown>(`/scenario-revisions/${revisionId}/upgrade-constraints`, { edges, expectedRevisionDigest })), 'scenario revision'));
  },
  async previewScenarioExecution(revisionId: string, input: ScenarioExecutionRequest) {
    return normalizeScenarioExecution(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/execution-plan`, input)), 'scenario execution plan'));
  },
  async scenarioAcceptance(revisionId: string, signal?: AbortSignal) {
    return normalizeScenarioAcceptance(requireRecord(unwrap(await get<unknown>(`/scenario-revisions/${revisionId}/acceptance`, signal)), 'scenario acceptance'));
  },
  async saveScenarioAcceptance(revisionId: string, input: Pick<ScenarioAcceptance, 'jobs' | 'parameters' | 'values' | 'bindings'> & { expectedRevisionDigest: string }) {
    return normalizeScenarioAcceptance(requireRecord(unwrap(await put<unknown>(`/scenario-revisions/${revisionId}/acceptance`, { ...input, jobs: input.jobs.map(({ legacyYamlSettings: _legacy, ...job }) => job) })), 'scenario acceptance'));
  },
  async scenarioAcceptanceFile(revisionId: string, path: string) {
    return normalizeWorkspaceFile(requireRecord(unwrap(await get<unknown>(`/scenario-revisions/${revisionId}/acceptance/workspace/file?${new URLSearchParams({ path })}`)), 'acceptance file'));
  },
  async saveScenarioAcceptanceFile(revisionId: string, input: { path: string; content: string; expectedSha256: string; expectedTreeSha256: string; expectedRevisionDigest: string; confirmYamlMigration?: boolean }) {
    return normalizeWorkspaceFile(requireRecord(unwrap(await put<unknown>(`/scenario-revisions/${revisionId}/acceptance/workspace/file`, input)), 'acceptance file'));
  },
  async uploadScenarioAcceptanceFile(revisionId: string, file: File, input: { path: string; expectedSha256: string; expectedTreeSha256: string; expectedRevisionDigest: string; confirmYamlMigration?: boolean }) {
    const form = new FormData(); form.set('file', file, file.name);
    for (const [key, value] of Object.entries(input)) form.set(key, String(value));
    return normalizeWorkspaceFile(requireRecord(unwrap(await postForm<unknown>(`/scenario-revisions/${revisionId}/acceptance/workspace/upload`, form)), 'acceptance file'));
  },
  async deleteScenarioAcceptanceFile(revisionId: string, input: { path: string; expectedSha256: string; expectedTreeSha256: string; expectedRevisionDigest: string }) {
    await request<unknown>(`/scenario-revisions/${revisionId}/acceptance/workspace/file?${new URLSearchParams(input)}`, { method: 'DELETE' });
  },
  async scenarios(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/scenarios', signal)).map((item) => normalizeScenario(requireRecord(item, 'scenario')));
  },
  async createScenario(input: Pick<Scenario, 'name'> & Partial<Scenario>) {
    return normalizeScenario(requireRecord(unwrap(await post<unknown>('/scenarios', input)), 'scenario'));
  },
  async deleteScenario(id: string) {
    await request<unknown>(`/scenarios/${id}`, { method: 'DELETE' });
  },
  async scenario(id: string) {
    return normalizeScenario(requireRecord(unwrap(await get<unknown>(`/scenarios/${id}`)), 'scenario'));
  },
  async previewScenarioClone(scenarioId: string, sourceRevisionId: string, sourceRunId?: string) {
    return unwrap(await post<unknown>(`/scenarios/${scenarioId}/revision-clone-plan`, { sourceRevisionId, sourceRunId })) as ScenarioClonePlan;
  },
  async cloneScenarioRevision(scenarioId: string, sourceRevisionId: string, expectedPlanDigest: string, sourceRunId?: string) {
    return normalizeRevision(requireRecord(unwrap(await post<unknown>(`/scenarios/${scenarioId}/revisions`, { sourceRevisionId, expectedPlanDigest, sourceRunId })), 'scenario revision'));
  },
  async abandonScenarioRevision(revisionId: string) {
    return normalizeScenario(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/abandon`)), 'scenario'));
  },
  async saveGraph(revisionId: string, graph: { nodes: ScenarioNode[]; edges: ScenarioEdge[]; environmentConstraints?: Record<string, unknown>; expectedDigest?: string }) {
    return normalizeRevision(requireRecord(normalizeOptionalData(await put<unknown>(`/scenario-revisions/${revisionId}/graph`, { ...scenarioGraphContent(graph), expectedDigest: graph.expectedDigest })), 'scenario revision'));
  },
  async validateScenario(revisionId: string) {
    const result = requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/validate`)), 'scenario validation');
    return { valid: requireBoolean(result, 'valid'), errors: requireStringArray(result, 'errors') };
  },
  async scenarioParameterOverview(revisionId: string, signal?: AbortSignal): Promise<ScenarioParameterOverview> {
    return unwrap(await get<unknown>(`/scenario-revisions/${revisionId}/parameter-overview`, signal)) as ScenarioParameterOverview;
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
  async previewScenarioJob(revisionId:string,environmentId:string,deliveryDecisions:Array<{requirementId:string;mode:string}>=[]):Promise<ComponentTestPlan> {
    return normalizeComponentTestPlan(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/job-plan`,{environmentId,deliveryDecisions})),'Job plan'));
  },
  async exportScenarioJob(revisionId:string,environmentId:string,expectedPlanDigest:string,deliveryDecisions:Array<{requirementId:string;mode:string}>=[]) {
    return download(`/scenario-revisions/${revisionId}/job-bundle`,{environmentId,expectedPlanDigest,deliveryDecisions});
  },
  runJobDownloadURL(runId:string, verified = false) { return `${API_ROOT}/runs/${encodeURIComponent(runId)}/job-bundle${verified ? "?verified=true" : ""}`; },
  async testScenario(revisionId: string, environmentId: string, execution?: Omit<ScenarioExecutionRequest, 'environmentId'>) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/test-runs`, { environmentId, ...execution })), 'run'));
  },
  async runScenario(revisionId: string, environmentId: string, execution?: Omit<ScenarioExecutionRequest, 'environmentId'>) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/runs`, { environmentId, ...execution })), 'run'));
  },
  async publishScenario(revisionId: string) {
    return normalizeRevision(requireRecord(normalizeOptionalData(await post<unknown>(`/scenario-revisions/${revisionId}/publish`)), 'scenario revision'));
  },
  async deprecateScenario(revisionId: string) {
    return normalizeRevision(requireRecord(unwrap(await post<unknown>(`/scenario-revisions/${revisionId}/deprecate`)), 'scenario revision'));
  },
  async environments(signal?: AbortSignal, includeArchived = false) {
    return unwrapList(await get<unknown>(includeArchived ? '/environments?includeArchived=true' : '/environments', signal)).map((item) => normalizeEnvironment(requireRecord(item, 'environment')));
  },
  async catalogRepository(signal?: AbortSignal): Promise<CatalogRepositoryStatus> {
    return normalizeCatalogRepository(unwrap(await get<unknown>('/catalog-repository', signal)));
  },
  async createCatalogRepository(path: string): Promise<CatalogRepositoryStatus> {
    return normalizeCatalogRepository(unwrap(await post<unknown>('/catalog-repository/create', { path })));
  },
  async connectCatalogRepository(path: string): Promise<CatalogRepositoryStatus> {
    return normalizeCatalogRepository(unwrap(await post<unknown>('/catalog-repository/connect', { path })));
  },
  async createCatalogBackup(): Promise<CatalogBackupResult> {
    return normalizeCatalogBackupResult(unwrap(await post<unknown>('/catalog-repository/backups')));
  },
  async previewCatalogRestore(ref: string): Promise<CatalogRestorePlan> {
    return normalizeCatalogRestorePlan(unwrap(await post<unknown>('/catalog-repository/restore-plan', { ref })));
  },
  async restoreCatalog(input: { ref: string; expectedPlanDigest: string; confirmation: string }): Promise<CatalogRecoveryResult> {
    const raw = requireRecord(unwrap(await post<unknown>('/catalog-repository/restore', input)), 'Catalog recovery result');
    return { ...normalizeCatalogRestorePlan(raw), restored: requireBoolean(raw, 'restored') };
  },
  async createEnvironment(input: Partial<Environment> & { facts?: Record<string, unknown> }) {
    return normalizeEnvironment(requireRecord(unwrap(await post<unknown>('/environments', input)), 'environment'));
  },
  async environmentLifecycle(environmentId: string) {
    return normalizeEnvironmentLifecycle(requireRecord(unwrap(await get<unknown>(`/environments/${environmentId}/lifecycle`)), 'environment lifecycle'));
  },
  async deleteEnvironment(environmentId: string) {
    await request<unknown>(`/environments/${environmentId}`, { method: 'DELETE' });
  },
  async environmentRevisionDeletionImpact(environmentId: string, revisionId: string, signal?: AbortSignal): Promise<EnvironmentRevisionDeletionImpact> {
    const raw = requireRecord(unwrap(await get<unknown>(`/environments/${environmentId}/revisions/${revisionId}/deletion-impact`, signal)), 'environment version deletion impact');
    return {
      environmentId: requireString(raw, 'environmentId'), environmentName: requireString(raw, 'environmentName'),
      revisionId: requireString(raw, 'revisionId'), revision: requireNumber(raw, 'revision'),
      current: requireBoolean(raw, 'current'), archived: requireBoolean(raw, 'archived'),
      runCount: requireNumber(raw, 'runCount'), imageBuildCount: requireNumber(raw, 'imageBuildCount'),
      healthCheckCount: requireNumber(raw, 'healthCheckCount'), sshCheckCount: requireNumber(raw, 'sshCheckCount'),
      canDelete: requireBoolean(raw, 'canDelete'),
      blockers: requireRecords(raw, 'blockers').map(item => ({ code: requireString(item, 'code'), message: requireString(item, 'message') })),
    };
  },
  async deleteEnvironmentRevision(environmentId: string, revisionId: string) {
    await request<unknown>(`/environments/${environmentId}/revisions/${revisionId}`, { method: 'DELETE' });
  },
  async archiveEnvironment(environmentId: string) {
    return normalizeEnvironment(requireRecord(unwrap(await post<unknown>(`/environments/${environmentId}/archive`)), 'environment'));
  },
  async unarchiveEnvironment(environmentId: string) {
    return normalizeEnvironment(requireRecord(unwrap(await post<unknown>(`/environments/${environmentId}/unarchive`)), 'environment'));
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
  async checkEnvironmentConnectivity(environmentId: string) {
    return normalizeEnvironmentConnectivityCheck(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/connectivity-checks`)), 'environment connectivity check'));
  },
  async previewEnvironmentRollback(environmentId: string, nodes?: string[]) {
    return normalizeEnvironmentRollbackPlan(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/cluster-rollback-plan`, { nodes })), 'environment rollback plan'));
  },
  async startEnvironmentRollback(environmentId: string, input: { expectedPlanDigest: string; confirmEnvironmentName: string; nodes?: string[] }) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/cluster-rollback-runs`, input)), 'run'));
  },
  async restoreEnvironmentRevision(environmentId: string, revisionId: string, changeReason: string) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await post<unknown>(`/environments/${environmentId}/revisions/${revisionId}/restore`, { changeReason })), 'environment'));
  },
  async environmentParameterFields(signal?: AbortSignal): Promise<EnvironmentParameterField[]> {
    return unwrapList(await get<unknown>('/environment-parameter-fields', signal)) as EnvironmentParameterField[];
  },
  async updateEnvironmentParameters(environmentId: string, values: Record<string, unknown>, changeReason: string) {
    return normalizeEnvironment(requireRecord(normalizeOptionalData(await put<unknown>(`/environments/${environmentId}/parameters`, { values, changeReason })), 'environment'));
  },
  async exportEnvironmentRevision(environmentId: string, revisionId: string, includeCredentialReferences: boolean) {
    return download(`/environments/${environmentId}/revisions/${revisionId}/export`, { includeCredentialReferences });
  },
  async previewEnvironmentImport(input: { document: EnvironmentExportDocument; target: { kind: 'new'; name: string; description?: string } | { kind: 'existing'; environmentId: string }; changeReason: string; confirmCredentialReferences?: boolean }) {
    return normalizeEnvironmentImportPlan(requireRecord(unwrap(await post<unknown>('/environment-imports/plan', input)), 'environment import plan'));
  },
  async importEnvironment(input: { document: EnvironmentExportDocument; target: { kind: 'new'; name: string; description?: string } | { kind: 'existing'; environmentId: string }; changeReason: string; confirmCredentialReferences?: boolean; expectedPlanDigest: string }) {
    return normalizeEnvironment(requireRecord(unwrap(await post<unknown>('/environment-imports', input)), 'environment'));
  },
  async auditEvents(signal?: AbortSignal) {
    return unwrapList(await get<unknown>('/audit-events', signal)).map((item) => normalizeAuditEvent(requireRecord(item, 'audit event')));
  },
  async runs(options: { page?: number; pageSize?: number; filter?: 'all' | 'active' | 'finished'; environmentId?: string; archive?: 'unarchived' | 'archived' | 'all' } = {}, signal?: AbortSignal): Promise<RunPage> {
    const query = new URLSearchParams({ page: String(options.page ?? 1), pageSize: String(options.pageSize ?? 50), filter: options.filter ?? 'all' });
    query.set('archive',options.archive ?? 'unarchived');
    if (options.environmentId) query.set('environmentId', options.environmentId);
    const raw = requireRecord(await get<unknown>(`/runs?${query}`, signal), 'run page');
    return { items: requireRecords(raw, 'items').map(normalizeRunSummary), page: requireNumber(raw, 'page'), pageSize: requireNumber(raw, 'pageSize'), total: requireNumber(raw, 'total') };
  },
  async batchApprovalCandidates(signal?: AbortSignal): Promise<RunSummary[]> {
    return unwrapList(await get<unknown>('/approvals/batch-candidates', signal)).map((item) => normalizeRunSummary(requireRecord(item, 'approval candidate')));
  },
  async run(id: string, signal?: AbortSignal) {
    return normalizeRun(requireRecord(unwrap(await get<unknown>(`/runs/${id}`, signal)), 'run'));
  },
  async runActivity(id: string, afterId?: number, signal?: AbortSignal): Promise<RunActivity> {
    const query = afterId === undefined ? '' : `?${new URLSearchParams({ afterId: String(afterId) })}`;
    const raw = requireRecord(unwrap(await get<unknown>(`/runs/${id}/activity${query}`, signal)), 'run activity');
    const runId = requireString(raw, 'runId');
    const nextAfterId = requireNumber(raw, 'nextAfterId');
    const logs = requireRecords(raw, 'logs').map(log => ({ id: requireNumber(log, 'id'), runId: requireString(log, 'runId'), stepId: optionalString(log, 'stepId') ?? '', stream: requireString(log, 'stream'), message: requireString(log, 'message'), createdAt: requireString(log, 'createdAt') }));
    const hasMore = requireBoolean(raw, 'hasMore');
    if (runId !== id || !Number.isSafeInteger(nextAfterId) || nextAfterId < (afterId ?? 0)
      || logs.some((log, index) => log.runId !== id || !Number.isSafeInteger(log.id) || log.id <= (index ? logs[index - 1].id : afterId ?? 0))
      || (logs.length && nextAfterId !== logs.at(-1)!.id) || (hasMore && !logs.length)) throw invalidResponse(200, '运行日志游标无效。');
    const waitingObservations: RunWaitingObservation[] = requireRecords(raw, 'waitingObservations').map(event => {
      const waiting = requireRecord(requireRecord(event.result, 'waiting result').waiting, 'waiting');
      return { host: optionalString(event, 'host') ?? '', task: optionalString(event, 'task') ?? '', stepId: requireString(event, 'stepId'), waiting: {
        object: optionalString(waiting, 'object'), expected: optionalString(waiting, 'expected'), observed: optionalString(waiting, 'observed'), attempt: optionalNumber(waiting, 'attempt'), deadline: optionalString(waiting, 'deadline'),
      } };
    });
    return { runId, status: requireEnum(raw, RUN_STATUSES, 'status'), logs, nextAfterId, hasMore, waitingObservations, archived: requireBoolean(raw, 'archived') };
  },
  async cancelRun(id: string) {
    return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/runs/${id}/cancel`)), 'run'));
  },
  async previewRunRetry(id: string) {
    return unwrap(await post<unknown>(`/runs/${id}/retry-plan`)) as RunRetryPlan;
  },
  async retryRun(id: string, expectedPlanDigest: string) {
    return normalizeRun(requireRecord(unwrap(await post<unknown>(`/runs/${id}/retry-runs`, { expectedPlanDigest })), 'run'));
  },
  async approve(id: string, reason = '', deliveryDecisions: Array<Pick<DeliveryDecision, 'requirementId' | 'mode'>> = []) {
	return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/approvals/${id}/approve`, { reason, deliveryDecisions })), 'run'));
  },
  async reject(id: string, reason = '') {
	return normalizeRun(requireRecord(normalizeOptionalData(await post<unknown>(`/approvals/${id}/reject`, { reason })), 'run'));
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

function normalizeLegacyYaml(raw: LooseRecord, legacyChecks: unknown): ActionDefinition['legacyYamlSettings'] {
  const gatherFacts = optionalBoolean(raw, 'gatherFacts') ?? false;
  const checks = (Array.isArray(legacyChecks) ? legacyChecks : []).map(value => {
    const check = requireRecord(value, 'legacy check');
    return { id: requireString(check, 'id'), kind: requireEnum(check, ['command','image_command','path_present','path_absent','service_inactive','network_rules_absent','tcp'] as const, 'kind'), target: requireString(check, 'target'), providedByReleaseId: optionalString(check, 'providedByReleaseId') };
  });
  return gatherFacts || checks.length ? { gatherFacts, checks } : undefined;
}
