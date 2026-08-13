import type {
  Component,
  ComponentRelease,
  DashboardSummary,
  Environment,
  ImpactPreview,
  Notification,
  Run,
  Scenario,
  ScenarioEdge,
  ScenarioNode,
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
  } catch {
    throw new ApiError(0, {
      error: { code: 'NETWORK_ERROR', message: '无法连接平台 API，请确认 Go 服务已启动。' },
    });
  }

  const text = await response.text();
  const payload = text ? safeJSON(text) : undefined;
  if (!response.ok) throw new ApiError(response.status, (payload ?? {}) as ApiErrorBody);
  return payload as T;
}

function safeJSON(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return { message: value };
  }
}

function unwrapList<T>(value: T[] | { items?: T[]; data?: T[] }): T[] {
  if (Array.isArray(value)) return value;
  return value.items ?? value.data ?? [];
}

function unwrap<T>(value: T | { data: T }): T {
  if (value && typeof value === 'object' && 'data' in value) return value.data;
  return value;
}

type LooseRecord = Record<string, any>;

function normalizeRelease(raw: LooseRecord): ComponentRelease {
  const status = raw.state ?? raw.status ?? 'draft';
  return {
    ...raw,
    id: raw.id,
    version: raw.version ?? '',
    componentId: raw.componentId ?? raw.component_id ?? '',
    state: status,
    status,
    releaseNotes: raw.releaseNotes ?? raw.release_notes,
    environmentConstraints: raw.environmentConstraints ?? raw.environment_constraints,
    parameterSchema: raw.parameterSchema ?? raw.parameter_schema,
    actions: (raw.actions ?? []).map((action: LooseRecord) => ({
      ...action,
      type: action.type ?? action.kind,
      hostGroup: action.hostGroup ?? action.host_group,
      timeoutSeconds: action.timeoutSeconds ?? action.timeout_seconds,
      allowedParameters: action.allowedParameters ?? action.allowed_parameters ?? [],
      fromReleaseId: action.fromReleaseId ?? action.from_release_id,
      toReleaseId: action.toReleaseId ?? action.to_release_id,
    })),
    dependencies: (raw.dependencies ?? []).map((dependency: LooseRecord) => ({
      ...dependency,
      componentId: dependency.componentId ?? dependency.component_id ?? dependency.upstreamComponentId,
      componentName: dependency.componentName ?? dependency.upstreamComponentName,
      releaseId: dependency.upstreamReleaseId ?? dependency.upstream_release_id ?? dependency.releaseId ?? dependency.release_id,
    })),
  };
}

function normalizeComponent(raw: LooseRecord): Component {
  const releases = (raw.releases ?? []).map(normalizeRelease) as ComponentRelease[];
  const latest = raw.latestRelease ?? raw.latest_release;
  const latestRelease = latest ? normalizeRelease(latest) : releases[0];
  return {
    ...raw,
    id: raw.id,
    name: raw.name ?? '',
    ownerId: raw.ownerId ?? raw.owner_id ?? raw.owner?.id ?? '',
    ownerName: raw.ownerName ?? raw.owner_name ?? raw.owner?.name,
    layer: raw.layer,
    category: raw.category,
    kind: raw.kind,
    requiredness: raw.requiredness,
    latestRelease,
    releases,
    releaseCount: raw.releaseCount ?? raw.release_count ?? releases.length,
  };
}

function normalizeRevision(raw?: LooseRecord) {
  if (!raw) return undefined;
  const graph = raw.graph ?? {};
  return {
    ...raw,
    scenarioId: raw.scenarioId ?? raw.scenario_id ?? '',
    state: raw.state ?? raw.status ?? 'draft',
    nodes: (raw.nodes ?? graph.nodes ?? []).map((node: LooseRecord) => node.data ? node : ({
      id: node.id,
      type: 'component',
      position: node.position ?? { x: 0, y: 0 },
      data: {
        label: node.name ?? node.label ?? node.id,
        componentId: node.componentId ?? '',
        releaseId: node.releaseId ?? '',
        version: node.version,
        action: node.action ?? 'install',
        hostGroup: node.hostGroup ?? 'all',
        values: node.values ?? {},
        bindings: node.bindings ?? {},
        runInputs: node.runInputs ?? [],
      },
    })),
    edges: raw.edges ?? graph.edges ?? [],
    runInputs: raw.runInputs ?? raw.run_inputs,
    executionPolicy: raw.executionPolicy ?? raw.execution_policy,
  };
}

function normalizeScenario(raw: LooseRecord): Scenario {
  const revisions = (raw.revisions ?? []).map(normalizeRevision);
  const currentID = raw.currentRevisionId ?? raw.current_revision_id;
  const current = raw.currentRevision ?? raw.current_revision ?? raw.revision ?? revisions.find((revision: LooseRecord) => revision?.id === currentID) ?? revisions[0];
  return {
    ...raw,
    id: raw.id,
    name: raw.name ?? '',
    ownerId: raw.ownerId ?? raw.owner_id ?? raw.owner?.id ?? '',
    ownerName: raw.ownerName ?? raw.owner_name ?? raw.owner?.name,
    currentRevision: current?.nodes ? current : normalizeRevision(current),
    revisions,
  } as Scenario;
}

function normalizeEnvironment(raw: LooseRecord): Environment {
  const revision = raw.currentRevision ?? raw.current_revision ?? raw.revision;
  let inventory = revision?.inventory ?? {};
  if (typeof inventory === 'string') {
    try { inventory = JSON.parse(inventory); } catch { inventory = {}; }
  }
  return {
    ...raw,
    id: raw.id,
    name: raw.name ?? '',
    ownerId: raw.ownerId ?? raw.owner_id ?? raw.owner?.id ?? '',
    ownerName: raw.ownerName ?? raw.owner_name ?? raw.owner?.name,
    status: raw.status ?? (raw.activeRunId ? 'locked' : 'ready'),
    currentRevision: revision
      ? {
          ...revision,
          environmentId: revision.environmentId ?? revision.environment_id ?? raw.id,
          hosts: revision.hosts ?? inventory.hosts ?? [],
          credentialRefs: (revision.credentialRefs ?? revision.credential_refs ?? []).map((credential: LooseRecord) => ({
            ...credential,
            type: credential.type ?? credential.kind,
            maskedReference: credential.reference || credential.configured ? '••••••••' : '未配置',
          })),
          maxConcurrentRuns: revision.maxConcurrentRuns ?? revision.max_concurrent_runs ?? revision.maxConcurrent,
        }
      : undefined,
  };
}

function normalizeRun(raw: LooseRecord): Run {
  const source = raw.run ?? raw;
  const logs = raw.logs ?? source.logs;
  return {
    ...source,
    id: source.id,
    status: source.status ?? 'queued',
    environmentId: source.environmentId ?? source.environment_id ?? '',
    environmentName: source.environmentName ?? source.environment_name,
    scenarioName: source.scenarioName ?? source.scenario_name,
    componentName: source.componentName ?? source.component_name,
    queuePosition: source.queuePosition ?? source.queue_position,
    createdBy: source.createdBy ?? source.requestedBy,
    createdByName: source.createdByName ?? source.created_by_name,
    logTail: source.logTail ?? source.log_tail ?? (Array.isArray(logs) ? logs.map((log: LooseRecord) => log.message ?? String(log)) : undefined),
    approval: source.approval ? { ...source.approval, riskReason: source.approval.riskReason ?? source.approval.reason } : undefined,
  };
}

function normalizeNotification(raw: LooseRecord): Notification {
  const payload = raw.payload ?? {};
  return {
    ...raw,
    id: raw.id,
    title: raw.title ?? '平台通知',
    message: raw.message ?? raw.body ?? '',
    read: raw.read ?? Boolean(raw.readAt),
    resourceUrl: raw.resourceUrl ?? raw.resource_url,
    componentId: raw.componentId ?? payload.componentId,
    componentName: raw.componentName ?? payload.componentName,
    oldVersion: raw.oldVersion ?? payload.oldVersion,
    newVersion: raw.newVersion ?? payload.newVersion,
    breaking: raw.breaking ?? payload.breaking,
    impactPaths: raw.impactPaths ?? payload.impactPaths ?? payload.paths,
    scenarioIds: raw.scenarioIds ?? payload.scenarioIds,
  };
}

const get = <T>(path: string) => request<T>(path);
const post = <T>(path: string, body?: unknown) =>
  request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
const put = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PUT', body: JSON.stringify(body) });
const patch = <T>(path: string, body: unknown) =>
  request<T>(path, { method: 'PATCH', body: JSON.stringify(body) });

export const api = {
  async me() {
    return unwrap(await get<User | { data: User }>('/session/me'));
  },
  async switchUser(userId: string) {
    return unwrap(await post<User | { data: User }>('/session/switch', { userId }));
  },
  async dashboard() {
    return unwrap(await get<DashboardSummary | { data: DashboardSummary }>('/dashboard'));
  },
  async components() {
    return unwrapList(await get<Component[] | { items?: Component[]; data?: Component[] }>('/components')).map((item) => normalizeComponent(item as LooseRecord));
  },
  async component(id: string) {
    return normalizeComponent(unwrap(await get<Component | { data: Component }>(`/components/${id}`)) as LooseRecord);
  },
  async createComponent(input: Partial<Component>) {
    return unwrap(await post<Component | { data: Component }>('/components', input));
  },
  async updateComponent(id: string, input: Partial<Component>) {
    return unwrap(await patch<Component | { data: Component }>(`/components/${id}`, input));
  },
  async cloneRelease(releaseId: string, input: { version: string; releaseNotes: string; breaking: boolean }) {
    return unwrap(
      await post<ComponentRelease | { data: ComponentRelease }>(`/component-releases/${releaseId}/clone`, input),
    );
  },
  async createRelease(componentId: string, input: Partial<ComponentRelease>) {
    return unwrap(
      await post<ComponentRelease | { data: ComponentRelease }>(`/components/${componentId}/releases`, input),
    );
  },
  async updateRelease(releaseId: string, input: Partial<ComponentRelease>) {
    return unwrap(
      await put<ComponentRelease | { data: ComponentRelease }>(`/component-releases/${releaseId}`, input),
    );
  },
  async releaseImpact(releaseId: string) {
    const raw = unwrap(await get<ImpactPreview | { data: ImpactPreview }>(`/component-releases/${releaseId}/impact`)) as unknown as LooseRecord;
    if (!raw.recipients) return raw as ImpactPreview;
    const componentOwners = raw.recipients.filter((item: LooseRecord) => item.role === 'component_owner').map((item: LooseRecord) => ({ id: item.userId, name: item.userName ?? item.userId }));
    const scenarioOwners = raw.recipients.filter((item: LooseRecord) => item.role === 'scenario_owner').map((item: LooseRecord) => ({ id: item.userId, name: item.userName ?? item.userId }));
    const scenarioIds = [...new Set(raw.recipients.flatMap((item: LooseRecord) => item.scenarioIds ?? []))] as string[];
    return { componentOwners, scenarioOwners, scenarios: scenarioIds.map((id) => ({ id, name: id })), paths: raw.recipients.flatMap((item: LooseRecord) => (item.paths ?? []).map((path: LooseRecord) => path.componentNames ?? path)) };
  },
  async publishRelease(releaseId: string) {
    return unwrap(
      await post<ComponentRelease | { data: ComponentRelease }>(`/component-releases/${releaseId}/publish`),
    );
  },
  async deprecateRelease(releaseId: string) {
    return normalizeRelease(unwrap(await post<ComponentRelease | { data: ComponentRelease }>(`/component-releases/${releaseId}/deprecate`)) as LooseRecord);
  },
  async testRelease(releaseId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return unwrap(
      await post<Run | { data: Run }>(`/component-releases/${releaseId}/test-runs`, { environmentId, runInput }),
    );
  },
  async scenarios() {
    return unwrapList(await get<Scenario[] | { items?: Scenario[]; data?: Scenario[] }>('/scenarios')).map((item) => normalizeScenario(item as LooseRecord));
  },
  async createScenario(input: Pick<Scenario, 'name'> & Partial<Scenario>) {
    return normalizeScenario(unwrap(await post<Scenario | { data: Scenario }>('/scenarios', input)) as LooseRecord);
  },
  async scenario(id: string) {
    return normalizeScenario(unwrap(await get<Scenario | { data: Scenario }>(`/scenarios/${id}`)) as LooseRecord);
  },
  async cloneScenarioRevision(scenarioId: string) {
    return normalizeRevision(unwrap(await post<LooseRecord | { data: LooseRecord }>(`/scenarios/${scenarioId}/revisions`)) as LooseRecord);
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
    return unwrap(
      await put<Scenario | { data: Scenario }>(`/scenario-revisions/${revisionId}/graph`, backendGraph),
    );
  },
  async validateScenario(revisionId: string) {
    const result = unwrap(
      await post<{ valid: boolean; errors: string[] } | { data: { valid: boolean; errors: string[] } }>(
        `/scenario-revisions/${revisionId}/validate`,
      ),
    );
    if (Array.isArray(result)) return { valid: result.length === 0, errors: result.map((issue: LooseRecord) => issue.message ?? String(issue)) };
    return result;
  },
  async testScenario(revisionId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return unwrap(
      await post<Run | { data: Run }>(`/scenario-revisions/${revisionId}/test-runs`, { environmentId, runInput }),
    );
  },
  async runScenario(revisionId: string, environmentId: string, runInput: Record<string, unknown> = {}) {
    return unwrap(
      await post<Run | { data: Run }>(`/scenario-revisions/${revisionId}/runs`, { environmentId, runInput }),
    );
  },
  async publishScenario(revisionId: string) {
    return unwrap(
      await post<Scenario | { data: Scenario }>(`/scenario-revisions/${revisionId}/publish`),
    );
  },
  async deprecateScenario(revisionId: string) {
    return normalizeRevision(unwrap(await post<LooseRecord | { data: LooseRecord }>(`/scenario-revisions/${revisionId}/deprecate`)) as LooseRecord);
  },
  async environments() {
    return unwrapList(await get<Environment[] | { items?: Environment[]; data?: Environment[] }>('/environments')).map((item) => normalizeEnvironment(item as LooseRecord));
  },
  async createEnvironment(input: Partial<Environment> & { facts?: Record<string, unknown> }) {
    return normalizeEnvironment(unwrap(await post<Environment | { data: Environment }>('/environments', input)) as LooseRecord);
  },
  async updateInventory(environmentId: string, hosts: Environment['currentRevision'] extends infer _ ? unknown : never) {
    return unwrap(await put<Environment | { data: Environment }>(`/environments/${environmentId}/inventory`, { hosts }));
  },
  async updateParameters(environmentId: string, parameters: Record<string, unknown>) {
    return unwrap(
      await put<Environment | { data: Environment }>(`/environments/${environmentId}/parameters`, { parameters }),
    );
  },
  async updateFacts(environmentId: string, facts: Record<string, unknown>) {
    return unwrap(await put<Environment | { data: Environment }>(`/environments/${environmentId}/facts`, { facts }));
  },
  async updateCredentialRefs(environmentId: string, credentialRefs: unknown[]) {
    const refs = credentialRefs.map((credential) => {
      const item = credential as LooseRecord;
      return { name: item.name, kind: item.kind ?? item.type, reference: item.reference };
    });
    return unwrap(
      await put<Environment | { data: Environment }>(`/environments/${environmentId}/credential-refs`, {
        credentialRefs: refs,
      }),
    );
  },
  async runs() {
    return unwrapList(await get<Run[] | { items?: Run[]; data?: Run[] }>('/runs')).map((item) => normalizeRun(item as LooseRecord));
  },
  async run(id: string) {
    return normalizeRun(unwrap(await get<Run | { data: Run }>(`/runs/${id}`)) as LooseRecord);
  },
  async cancelRun(id: string) {
    return unwrap(await post<Run | { data: Run }>(`/runs/${id}/cancel`));
  },
  async approve(id: string) {
    return unwrap(await post<Run | { data: Run }>(`/approvals/${id}/approve`));
  },
  async reject(id: string) {
    return unwrap(await post<Run | { data: Run }>(`/approvals/${id}/reject`));
  },
  async notifications() {
    return unwrapList(
      await get<Notification[] | { items?: Notification[]; data?: Notification[] }>('/notifications'),
    ).map((item) => normalizeNotification(item as LooseRecord));
  },
  async markNotificationRead(id: string, read = true) {
    return unwrap(await patch<Notification | { data: Notification }>(`/notifications/${id}`, { read }));
  },
};

export function eventsURL(): string {
  return `${API_ROOT}/events`;
}
