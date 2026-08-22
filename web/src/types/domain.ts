export type Role = 'component_owner' | 'scenario_owner' | 'environment_owner';

export interface User {
  id: string;
  name: string;
  role: Role;
  title?: string;
}

export type ReleaseState = 'draft' | 'released' | 'deprecated';
export type VerificationState = 'unverified' | 'testing' | 'passed' | 'failed';
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
  environmentPath?: string;
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
  risk?: 'normal' | 'destructive';
  riskLevel?: 'low' | 'medium' | 'high' | 'destructive';
  destructive?: boolean;
  fromReleaseId?: string;
  toReleaseId?: string;
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
  status?: ReleaseState;
  verification?: VerificationState;
  verified?: boolean;
  breaking?: boolean;
  releaseNotes?: string;
  dependencies?: ComponentDependency[];
  environmentConstraints?: Record<string, unknown>;
  parameters?: ParameterDefinition[];
  actions?: ActionDefinition[];
  createdAt?: string;
  releasedAt?: string;
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

export type ScenarioState = 'draft' | 'testing' | 'test_passed' | 'released' | 'deprecated';

export interface ScenarioNodeData extends Record<string, unknown> {
  label: string;
  componentId: string;
  releaseId: string;
  version?: string;
  action?: ActionDefinition['type'];
  hostGroup?: string;
  values?: Record<string, unknown>;
  bindings?: Record<string, string>;
  runInputs?: string[];
  dependencySources?: Record<string, string>;
  layer?: ComponentLayer;
}

export interface ScenarioNode {
  id: string;
  type?: string;
  position: { x: number; y: number };
  data: ScenarioNodeData;
}

export interface ScenarioEdge {
  id: string;
  source: string;
  target: string;
  label?: string;
}

export interface ScenarioRevision {
  id: string;
  scenarioId: string;
  revision: number;
  version?: string;
  state: ScenarioState;
  nodes: ScenarioNode[];
  edges: ScenarioEdge[];
  runInputs?: string[];
  executionPolicy?: Record<string, unknown>;
  validationErrors?: string[];
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
  currentRevision?: ScenarioRevision;
  revisions?: ScenarioRevision[];
  updatedAt?: string;
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
  parameters: Record<string, unknown>;
  credentialRefs: CredentialRef[];
  maxConcurrentRuns?: number;
  createdAt?: string;
}

export interface Environment {
  id: string;
  name: string;
  description?: string;
  ownerId: string;
  ownerName?: string;
  status?: 'ready' | 'locked' | 'offline';
  activeRunId?: string;
  currentRevision?: EnvironmentRevision;
  updatedAt?: string;
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
  kind?: 'component_test' | 'scenario_test' | 'scenario_run';
  name?: string;
  status: RunStatus;
  scenarioId?: string;
  scenarioName?: string;
  scenarioRevisionId?: string;
  componentId?: string;
  componentName?: string;
  componentReleaseId?: string;
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
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
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
