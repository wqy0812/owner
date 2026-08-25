export type Role = 'component_owner' | 'scenario_owner' | 'environment_owner';

export interface User {
  id: string;
  name: string;
  role: Role;
  title?: string;
}

export type ReleaseState = 'draft' | 'released' | 'deprecated';
export type ComponentLayer = 'host_foundation' | 'runtime_state' | 'orchestration_core' | 'cluster_service' | 'observability_management' | 'platform_extension';
export type ComponentCategory = 'preflight' | 'bootstrap' | 'security' | 'runtime' | 'state_store' | 'control_plane' | 'worker' | 'network' | 'dns' | 'ingress' | 'storage' | 'observability' | 'node_management' | 'platform' | 'autoscaling';
export type ComponentKind = 'software' | 'software_bundle' | 'delivery_stage' | 'configuration' | 'artifact_set';
export type ComponentRequiredness = 'core_required' | 'profile_required' | 'optional';

export type ParameterType = 'string' | 'boolean' | 'integer' | 'number' | 'object' | 'array';
export type ParameterVisibility = 'internal' | 'public';

export interface ParameterDefinition {
  name: string;
  description: string;
  type: ParameterType;
  required?: boolean;
  defaultValue?: unknown;
  visibility: ParameterVisibility;
  enum?: unknown[];
  minLength?: number;
}

export interface ParameterMapping {
  upstreamParameter: string;
  targetParameter: string;
}

export interface ComponentDependency {
  id?: string;
  componentId: string;
  componentName?: string;
  releaseId: string;
  version?: string;
  purpose?: string;
  parameterMappings?: ParameterMapping[];
}

export interface ResolvedParameter {
  value?: unknown;
  source?: string;
  sourceNodeId?: string;
  upstreamParameter?: string;
  targetParameter?: string;
}

export interface ActionDefinition {
  id?: string;
  name?: string;
  type: 'inspect' | 'preflight' | 'install' | 'configure' | 'upgrade' | 'verify' | 'rollback' | 'uninstall';
  playbook: string;
  tags?: string[];
  limit?: string;
  hostGroup?: string;
  timeoutSeconds?: number;
  allowedParameters?: string[];
  requiredCredentials?: string[];
  riskLevel?: 'low' | 'medium' | 'high' | 'destructive';
  destructive?: boolean;
  idempotent?: boolean;
  fromReleaseId?: string;
  toReleaseId?: string;
}

export function executableActionTypes(actions: ActionDefinition[] = []): ActionDefinition['type'][] {
  const types = actions.map((action) => action.type);
  if (actions.some((action) => action.type === 'install' && action.idempotent)) types.push('upgrade');
  return [...new Set(types)];
}

export interface PlaybookFile {
  path: string;
  filename: string;
  content: string;
  sha256: string;
  updatedAt?: string;
}

export interface ComponentRelease {
  id: string;
  componentId: string;
  version: string;
  type?: 'atomic' | 'bundle';
  state: ReleaseState;
  verified?: boolean;
  candidate?: boolean;
  breaking?: boolean;
  releaseNotes?: string;
  riskLevel?: 'low' | 'medium' | 'high' | 'destructive';
  dependencies?: ComponentDependency[];
  environmentConstraints?: Record<string, unknown>;
  parameters?: ParameterDefinition[];
  actions?: ActionDefinition[];
  artifacts?: ComponentArtifact[];
  createdAt?: string;
  releasedAt?: string;
}

export interface ComponentArtifact {
  id: string;
  releaseId: string;
  alias: string;
  fileStation: string;
  relativePath: string;
  filename: string;
  sha256: string;
  sizeBytes: number;
  sourceMode: 'upload' | 'register';
  environmentId: string;
  environmentRevisionId: string;
  createdBy: string;
  createdAt: string;
}

export type ImageBuildStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted';

export interface ImageBuildLog {
  id: number;
  buildId: string;
  stream: 'stdout' | 'stderr' | 'system';
  message: string;
  createdAt: string;
}

export interface ComponentImageBuild {
  id: string;
  releaseId: string;
  environmentId?: string;
  environmentRevisionId?: string;
  requestedBy: string;
  status: ImageBuildStatus;
  dockerfileSha256: string;
  imageTag: string;
  imageRef: string;
  imageDigest?: string;
  error?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  logs?: ImageBuildLog[];
}

export interface Component {
  id: string;
  name: string;
  slug?: string;
  description?: string;
  ownerId: string;
  ownerName?: string;
  layer: ComponentLayer;
  category: ComponentCategory;
  kind: ComponentKind;
  requiredness: ComponentRequiredness;
  latestRelease?: ComponentRelease;
  releases?: ComponentRelease[];
  releaseCount?: number;
  updatedAt?: string;
}

export type ScenarioState = 'draft' | 'testing' | 'test_passed' | 'released' | 'deprecated' | 'abandoned';

export interface ScenarioNodeData extends Record<string, unknown> {
  label: string;
  componentId: string;
  releaseId: string;
  version?: string;
  action?: ActionDefinition['type'];
  hostGroup?: string;
  values?: Record<string, unknown>;
  runInputs?: string[];
  dependencySources?: Record<string, string>;
  layer?: ComponentLayer;
}

export interface ScenarioNode {
  id: string;
  type: 'component';
  position: { x: number; y: number };
  data: ScenarioNodeData;
}

export interface ScenarioEdge {
  id: string;
  source: string;
  target: string;
}

export interface ScenarioRevision {
  id: string;
  scenarioId: string;
  revision: number;
  state: ScenarioState;
  nodes: ScenarioNode[];
  edges: ScenarioEdge[];
  executionPolicy?: Record<string, unknown>;
  testedAt?: string;
  createdAt?: string;
}

export interface Scenario {
  id: string;
  slug: string;
  name: string;
  description?: string;
  ownerId: string;
  ownerName?: string;
  currentRevisionId?: string;
  currentRevision?: ScenarioRevision;
  revisions?: ScenarioRevision[];
  updatedAt?: string;
}

export interface CandidateReleaseSet {
  scenarioRevisionId: string;
  ready: boolean;
  releases: Array<{ releaseId: string; componentId: string; componentName: string; version: string }>;
  issues: Array<{ code: string; message: string; nodeId?: string }>;
}

export interface EnvironmentHost {
  name: string;
  address: string;
  groups: string[];
  port?: number;
  user?: string;
}

export interface CredentialRef {
  name: string;
  type: 'sshKeyPath' | 'envVarRef';
  reference?: string;
  maskedReference?: string;
}

export interface EnvironmentRevision {
  id: string;
  environmentId: string;
  revision: number;
  facts: Record<string, unknown>;
  hosts: EnvironmentHost[];
  variables: Record<string, string>;
  credentialRefs: CredentialRef[];
  maxConcurrentRuns?: number;
  createdBy?: string;
  changeReason?: string;
  createdAt?: string;
}

export interface EnvironmentEndpointCheck {
  kind: 'host' | 'dependency' | 'configuration';
  name: string;
  address: string;
  reachable: boolean;
  latencyMs: number;
  error?: string;
}

export interface EnvironmentHealthCheck {
  id: string;
  environmentId: string;
  environmentRevisionId: string;
  status: 'healthy' | 'degraded';
  results: EnvironmentEndpointCheck[];
  checkedAt: string;
}

export interface Environment {
  id: string;
  name: string;
  description?: string;
  ownerId: string;
  ownerName?: string;
  status?: 'ready' | 'locked' | 'offline';
  schedulingStatus?: 'idle' | 'queued' | 'awaiting_approval' | 'running';
  activeRunId?: string;
  currentRevision?: EnvironmentRevision;
  revisions?: EnvironmentRevision[];
  healthCheck?: EnvironmentHealthCheck;
  updatedAt?: string;
}

export interface AuditEvent {
  id: string;
  actorId: string;
  action: string;
  resourceType: string;
  resourceId: string;
  metadata: Record<string, unknown>;
  createdAt: string;
}

export type WorkPriority = 'critical' | 'high' | 'normal' | 'info';
export type WorkStatus = 'blocked' | 'action_required' | 'in_progress' | 'attention';

export interface WorkAction {
  label: string;
  href: string;
}

export interface WorkCause {
  kind: string;
  summary: string;
  actorId?: string;
  actorName?: string;
  action?: string;
  at?: string;
}

export interface WorkReason {
  code: string;
  message: string;
  evidenceRunId?: string;
  cause?: WorkCause;
  nextAction?: WorkAction;
}

export interface WorkExplanation {
  reasons: WorkReason[];
  primaryAction?: WorkAction;
  secondaryActions: WorkAction[];
}

export interface WorkItem {
  id: string;
  kind: 'component_draft' | 'scenario_revision' | 'environment' | 'run' | 'upstream_impact';
  priority: WorkPriority;
  status: WorkStatus;
  title: string;
  subject: {
    type: string;
    id: string;
    parentId?: string;
    name: string;
    version?: string;
    revision?: number;
    environment?: string;
  };
  reasons: WorkReason[];
  primaryAction: WorkAction;
  secondaryActions: WorkAction[];
  updatedAt: string;
}

export interface Workbench {
  generatedAt: string;
  role: Role;
  summary: { critical: number; actionRequired: number; inProgress: number; informational: number };
  assets: { components: number; scenarios: number; environments: number };
  items: WorkItem[];
}

export type RunStatus =
  | 'queued'
  | 'awaiting_approval'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'interrupted'
  | 'rejected';

export interface RunStep {
  id: string;
  name: string;
  componentName?: string;
  action?: string;
  status: RunStatus | 'pending' | 'skipped';
  startedAt?: string;
  finishedAt?: string;
  summary?: string;
}

export interface RunBackup {
  nodeId?: string;
  componentId: string;
  componentName?: string;
  releaseId: string;
  action: string;
  backupRef: string;
  installRunId: string;
  capturedAt: string;
  playbookSha256: string;
}

export interface Approval {
  id: string;
  runId: string;
  status: 'pending' | 'approved' | 'rejected';
  riskReason?: string;
  requestedAt?: string;
  decidedAt?: string;
}

export interface Run {
  id: string;
  kind?: 'component_test' | 'scenario_test' | 'scenario_run' | 'environment_rollback';
  name?: string;
  status: RunStatus;
  scenarioId?: string;
  scenarioName?: string;
  scenarioRevisionId?: string;
  componentId?: string;
  componentName?: string;
  componentReleaseId?: string;
  action?: string;
  environmentId: string;
  environmentName?: string;
  createdBy?: string;
  createdByName?: string;
  destructive?: boolean;
  queuePosition?: number;
  progress?: number;
  steps?: RunStep[];
  approval?: Approval;
  logTail?: string[];
  resolvedParametersByNode?: Record<string, Record<string, ResolvedParameter>>;
  backups?: RunBackup[];
  artifactTransfers?: Array<{ alias: string; sourceStation: string; targetStation: string; relativePath: string; sha256: string }>;
  imageTransfers?: Array<{ sourceRegistry: string; targetRegistry: string; sourceDigest: string; targetDigest: string }>;
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
}

export type ComponentTestMode = 'install_verify' | 'rollback';

export type RollbackVerification =
  | { kind: 'target_release'; releaseId: string }
  | { kind: 'rollback_only' };

export interface ComponentTestRequest {
  environmentId: string;
  mode: ComponentTestMode;
  rollbackVerification?: RollbackVerification;
  runInput?: Record<string, unknown>;
  dependencyFixtures?: Record<string, unknown>;
  expectedPlanDigest?: string;
}

export interface ComponentTestPlanStep {
  order: number;
  componentId: string;
  componentName: string;
  releaseId: string;
  releaseVersion: string;
  action: ActionDefinition['type'];
  playbook: string;
  limit?: string;
  needsApproval: boolean;
  fromReleaseId?: string;
  fromReleaseVersion?: string;
  toReleaseId?: string;
  toReleaseVersion?: string;
  backupRef?: string;
  backupInstallRunId?: string;
  backupCapturedAt?: string;
  backupPlaybookSha256?: string;
}

export interface ComponentTestPlan {
  environmentId: string;
  environmentRevisionId: string;
  destructive: boolean;
  requiresApproval: boolean;
  planDigest: string;
  steps: ComponentTestPlanStep[];
}

export interface EnvironmentRollbackPlan extends ComponentTestPlan {
  environmentName: string;
  sources: Array<{ runId: string; kind: NonNullable<Run['kind']>; scenarioRevisionId?: string; componentCount: number }>;
  componentCount: number;
  nodeCount: number;
}

export interface Notification {
  id: string;
  userId?: string;
  type?: string;
  title: string;
  message: string;
  read: boolean;
  resourceUrl?: string;
  componentId?: string;
  componentName?: string;
  oldVersion?: string;
  newVersion?: string;
  breaking?: boolean;
  impactPaths?: string[][];
  scenarioIds?: string[];
  createdAt?: string;
}

export interface ImpactPreview {
  componentOwners: Array<{ id: string; name: string }>;
  scenarioOwners: Array<{ id: string; name: string }>;
  scenarios: Array<{ id: string; name: string }>;
  paths: string[][];
}

export const ROLE_LABELS: Record<Role, string> = {
  component_owner: '组件 Owner',
  scenario_owner: '场景 Owner',
  environment_owner: '环境 Owner',
};

export const STATUS_LABELS: Record<string, string> = {
  draft: '草稿',
  released: '已发布',
  deprecated: '已废弃',
  abandoned: '已放弃',
  unverified: '未验证',
  testing: '测试中',
  test_passed: '测试通过',
  passed: '已验证',
  failed: '失败',
  queued: '排队中',
  awaiting_approval: '等待审批',
  running: '执行中',
  succeeded: '成功',
  cancelled: '已取消',
  interrupted: '已中断',
  rejected: '已拒绝',
  pending: '待执行',
  skipped: '已跳过',
  approved: '已批准',
  ready: '可用',
  locked: '占用中',
  offline: '离线',
};
