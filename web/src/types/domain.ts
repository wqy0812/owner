export type Role = 'component_owner' | 'scenario_owner' | 'environment_owner' | 'platform_admin';

export interface PlatformOptionUsage {
  componentReleases: number;
  scenarioRevisions: number;
  environmentRevisions: number;
}

export interface PlatformOption {
  id: string;
  categoryId: string;
  parentOptionId?: string;
  value: string;
  label: string;
  retiredAt?: string;
  sortOrder: number;
  createdBy: string;
  createdAt: string;
  usage: PlatformOptionUsage;
}

export interface PlatformOptionCategory {
  id: string;
  key: string;
  label: string;
  parentCategoryId?: string;
  kind: 'environment_dimension' | 'host_group';
  environmentRequired: boolean;
  retiredAt?: string;
  sortOrder: number;
  createdBy: string;
  createdAt: string;
  usage: PlatformOptionUsage;
  options: PlatformOption[];
}

export interface User {
  id: string;
  name: string;
  role: Role;
}

export type ReleaseState = 'draft' | 'released' | 'deprecated';
export type ComponentLayer = 'host_foundation' | 'runtime_state' | 'orchestration_core' | 'cluster_service' | 'observability_management' | 'platform_extension';

export type ParameterType = 'string' | 'boolean' | 'integer' | 'number' | 'object' | 'array';
export type ParameterVisibility = 'internal' | 'public';
export type ParameterValueProvider = 'component_owner' | 'scenario_owner' | 'environment_owner' | 'upstream_mapping';

export interface EnvironmentParameterBinding {
  kind: 'private';
}

export interface ParameterDefinition {
  name: string;
  description: string;
  type: ParameterType;
  required?: boolean;
  visibility: ParameterVisibility;
  modifiable: boolean;
  valueProvider: ParameterValueProvider;
  fixedValue?: unknown;
  suggestedValue?: unknown;
  testValue?: unknown;
  environmentBinding?: EnvironmentParameterBinding;
  enum?: unknown[];
  minLength?: number;
}

export interface EnvironmentVariableDefinition {
  id: string;
  name: string;
  label: string;
  description?: string;
  usage: number;
  createdBy: string;
  createdAt: string;
}

export interface ParameterMapping {
  upstreamParameter: string;
  targetParameter: string;
}

export interface ComponentDependency {
  kind?: 'configuration';
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

export interface RuntimeCheck { id: string; kind: 'command' | 'image_command' | 'path_present' | 'path_absent' | 'service_inactive' | 'network_rules_absent' | 'tcp'; target: string; providedByReleaseId?: string }
export interface LegacyYamlSettings { readonly gatherFacts: boolean; readonly checks: RuntimeCheck[] }
export interface ResourceContract { version: 1; noManagedPaths: boolean; claims: ResourceClaim[] }
export interface ResourceClaim { id: string; path: string; scope: 'file' | 'tree'; access: 'manage' | 'read' | 'verify'; exclusive?: boolean; excludes?: string[]; sharedPaths?: string[]; sharedWith?: { releaseId: string; claimId: string } }
export interface ActionDefinition {
  resourceContract?: ResourceContract;
  preCheckActionId?: string;
  postCheckActionId?: string;
  become?: boolean;
  legacyYamlSettings?: LegacyYamlSettings;
  id?: string;
  name?: string;
  type: 'check' | 'inspect' | 'preflight' | 'install' | 'configure' | 'upgrade' | 'verify' | 'rollback' | 'uninstall';
  playbook: string;
  tags?: string[];
  hostGroup?: string;
  timeoutSeconds?: number;
  requiredCredentials?: string[];
  riskLevel?: 'low' | 'medium' | 'high' | 'destructive';
  destructive?: boolean;
  idempotent?: boolean;
  fromReleaseId?: string;
  toReleaseId?: string;
}

export function executableActionTypes(actions: ActionDefinition[] = []): ActionDefinition['type'][] {
  const types = actions.filter((action) => action.type !== 'check').map((action) => action.type);
  if (actions.some((action) => action.type === 'install' && action.idempotent)) types.push('upgrade');
  return [...new Set(types)];
}

export interface PlaybookFile {
  path: string;
  filename: string;
  content: string;
  sha256: string;
  updatedAt?: string;
  action?: ActionDefinition;
}

export interface PlaybookWorkspaceFile {
  releaseId: string;
  path: string;
  sha256: string;
  sizeBytes: number;
  mediaType: string;
  updatedAt?: string;
  editable?: boolean;
  content?: string;
}

export interface WorkspaceReference {
  actions: Array<{ actionId: string; actionName: string; usedAs: string[] }>;
  staticReferences: string[];
  dynamicReferencesUnknown: boolean;
  protectionReason?: string;
}
export interface PlaybookWorkspace {
  references?: Record<string, WorkspaceReference>;
  root: string;
  treeSha256: string;
  files: PlaybookWorkspaceFile[];
}

export interface ReleaseReviewPreview {
  componentId: string;
  componentName: string;
  ownerId: string;
  ownerName: string;
  release: ComponentRelease;
  playbooks: Array<{
    actionId: string;
    actionName: string;
    actionKind: ActionDefinition['type'];
    path: string;
    filename: string;
    content: string;
    sha256: string;
  }>;
  previewDigest: string;
}

export interface ComponentRelease {
  definitionGeneration?: number;
  id: string;
  componentId: string;
  lineId: string;
  lineName: string;
  parentReleaseId?: string;
  templateSourceReleaseId?: string;
  version: string;
  state: ReleaseState;
  candidate?: boolean;
  review: {
    status: 'not_submitted' | 'pending' | 'approved' | 'rejected';
    contractDigest?: string;
    submittedAt?: string;
    reviewedBy?: string;
    reviewedAt?: string;
    comment?: string;
  };
  readiness: ReleaseReadiness;
  compatibility: 'not_applicable' | 'compatible' | 'breaking';
  releaseNotes?: string;
  riskLevel?: 'low' | 'medium' | 'high' | 'destructive';
  dependencies?: ComponentDependency[];
  environmentConstraints?: Record<string, unknown>;
  parameters?: ParameterDefinition[];
  actions?: ActionDefinition[];
  artifacts?: ComponentArtifact[];
  images?: ComponentImage[];
  playbookFiles?: PlaybookWorkspaceFile[];
  playbookFileCount?: number;
  playbookTreeSha256?: string;
  playbookWorkspaceRoot?: string;
  createdAt?: string;
  releasedAt?: string;
  deprecatedAt?: string;
}

export interface ReleaseReadiness {
  status: 'ready' | 'blocked' | 'risky';
  blockers: Array<{ code: string; message: string; actionUrl: string }>;
  installEvidenceRunId?: string;
  rollbackEvidenceRunId?: string;
  transitionEvidenceRunId?: string;
}



export interface ComponentReleaseLine {
  environmentConstraints?: Record<string, unknown>;
  id: string;
  componentId: string;
  name: string;
  latestReleasedId?: string;
  currentDraftId?: string;
  evolutionEligible: boolean;
  evolutionParentId?: string;
  evolutionBlockedReason?: string;
  releases: ComponentRelease[];
  createdAt: string;
}

export interface ComponentArtifact {
  id: string;
  releaseId: string;
  alias: string;
  filename: string;
  sha256: string;
  sizeBytes: number;
  sourceUrl: string;
  sourceUpdatedBy: string;
  sourceUpdatedAt: string;
  createdBy: string;
  createdAt: string;
}

export interface ComponentImage {
  id: string;
  releaseId: string;
  logicalName: string;
  digest: string;
  sourceRef: string;
  sourceUpdatedBy: string;
  sourceUpdatedAt: string;
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

export interface EvidenceSummary {
  id: string;
  status: RunStatus;
  environmentId: string;
  environmentName: string;
  createdAt: string;
  finishedAt?: string;
  matchesContract: boolean;
}

export interface ReleaseEvidenceSummary {
  currentInstall?: EvidenceSummary;
  currentRollback?: EvidenceSummary;
  currentTransition?: EvidenceSummary;
  historicalInstall?: EvidenceSummary;
  historicalRollback?: EvidenceSummary;
  historicalTransition?: EvidenceSummary;
  currentById: Record<string, EvidenceSummary>;
}

export interface ParameterConsumer {
  componentName: string;
  version?: string;
  upstreamParameter: string;
  targetParameter: string;
  label: string;
}

export interface ComponentSummary {
  id: string;
  name: string;
  slug?: string;
  description?: string;
  ownerId: string;
  ownerName: string;
  layer: ComponentLayer;
  tags: string[];
  releaseCount: number;
  defaultReleaseId?: string;
  hasDraft: boolean;
  needsAttention: boolean;
}

export interface RunSummary {
  archiveStatus?: string;
  archivedAt?: string;
  archiveSizeBytes?: number;
  id: string;
  kind?: Run['kind'];
  status: RunStatus;
  name: string;
  scenarioName?: string;
  componentName?: string;
  componentReleaseId?: string;
  scenarioId?: string;
  action?: ActionDefinition['type'];
  environmentId: string;
  environmentName: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  queuePosition?: number;
  approvalId?: string;
}

export interface RunPage {
  items: RunSummary[];
  page: number;
  pageSize: number;
  total: number;
}

export interface Component {
  id: string;
  readContext?: {
    evidence: Record<string, ReleaseEvidenceSummary>;
    workItems: WorkItem[];
    parameterConsumers: ParameterConsumer[];
  };
  name: string;
  slug?: string;
  description?: string;
  ownerId: string;
  ownerName?: string;
  layer: ComponentLayer;
  tags: string[];
  latestRelease?: ComponentRelease;
  releases?: ComponentRelease[];
  releaseLines?: ComponentReleaseLine[];
  releaseCount?: number;
  updatedAt?: string;
}

export type ScenarioState = 'draft' | 'testing' | 'test_passed' | 'released' | 'deprecated' | 'abandoned';

export interface ScenarioNodeData extends Record<string, unknown> {
  contractAvailability?: 'available' | 'unshared' | 'missing';
  componentOwnerId?: string;
  componentOwnerName?: string;
  label: string;
  componentId: string;
  releaseId: string;
  version?: string;
  action?: ActionDefinition['type'];
  hostGroup?: string;
  parameterValues?: Record<string, unknown>;
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
  kind?: 'dependency' | 'sequence';
  dependencyId?: string;
}

export type ScenarioExecutionMode = 'install' | 'upgrade' | 'baseline_verify';

export interface ScenarioAcceptanceJob {
 legacyYamlSettings?: LegacyYamlSettings;
  id: string; name: string; purpose: string; hostGroup: string; timeoutSeconds: number;
  riskLevel: 'low' | 'medium' | 'high' | 'destructive'; requiredCredentials: string[];
  become: boolean; playbook: string; playbookSha256: string; mayMutate: boolean;
}
export interface ScenarioParameterBinding {
  parameter: string; source: 'node' | 'environment'; nodeId?: string; sourceParameter: string;
}
export interface ScenarioAcceptance {
  revisionId: string; revisionDigest: string; editable: boolean;
  jobs: ScenarioAcceptanceJob[]; parameters: ParameterDefinition[]; values: Record<string, unknown>;
  bindings: ScenarioParameterBinding[]; workspace: PlaybookWorkspace;
}
export interface ScenarioForkInput { environmentConstraints?: Record<string, unknown>; sourceRevisionId: string; name: string; slug: string; description: string; expectedPlanDigest?: string }
export interface ScenarioForkPlan {
 environmentConstraints?: Record<string,unknown>; sourceEnvironmentConstraints?: Record<string,unknown>;
  sourceScenarioId: string; sourceRevisionId: string; sourceRevision: number; sourceDigest: string;
  nodeCount: number; acceptanceJobCount: number; planDigest: string;
}
export interface ScenarioClonePlan {
  scenarioId: string; sourceRevisionId: string; sourceRunId: string; sourceRevision: number;
  nextRevision: number; nodeCount: number; edgeCount: number; planDigest: string;
}
export interface ScenarioExecutionRequest {
  environmentId: string; executionMode: ScenarioExecutionMode; expectedPlanDigest?: string;
  idempotencyKey?: string; testOnly?: boolean;
}
export interface ScenarioExecutionPreview {
  scenarioRevisionId: string; environmentId: string; executionMode: ScenarioExecutionMode;
  planDigest: string; sourceRevisionId?: string; baselineRunId?: string; ready: boolean; needsApproval?: boolean;
  operations: Array<{ nodeId: string; name: string; change: string; fromReleaseId?: string; toReleaseId?: string }>;
  steps: Array<{ nodeId?: string; name?: string; action?: string; phase?: string; kind?: string; playbook?: string; hostGroup?: string }>;
  issues: Array<{ code: string; message: string; nodeId?: string }>;
  installationTest?: ScenarioTestEvidence; upgradeTest?: ScenarioTestEvidence;
}
export interface ScenarioTestEvidence { runId?: string; valid: boolean; reason?: string; testedAt?: string }

export interface ScenarioRevision {
  sourceRevisionId?: string; sourceRunId?: string; revisionDigest?: string; digestVersion?: number;
  upgradeConstraints?: ScenarioEdge[]; acceptanceJobs?: ScenarioAcceptanceJob[];
  acceptanceParameters?: ParameterDefinition[]; acceptanceValues?: Record<string, unknown>;
  acceptanceBindings?: ScenarioParameterBinding[]; acceptanceWorkspaceRoot?: string; acceptanceTreeSha256?: string;
  installationTest?: ScenarioTestEvidence; upgradeTest?: ScenarioTestEvidence;
  environmentConstraints?: Record<string, unknown>;
  id: string;
  scenarioId: string;
  revision: number;
  state: ScenarioState;
  nodes: ScenarioNode[];
  edges: ScenarioEdge[];
  testedAt?: string;
  createdAt?: string;
}

export interface Scenario {
  environmentConstraints?: Record<string, unknown>;
  forkedFromScenarioId?: string; forkedFromRevisionId?: string; forkedFromDigest?: string;
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
  configured?: boolean;
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
  variables: Record<string, string>;
  credentialRefs: CredentialRef[];
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

export interface EnvironmentSSHHostCheck {
  kind: 'host' | 'configuration';
  name: string;
  address: string;
  user?: string;
  status: 'passed' | 'unreachable' | 'failed' | 'skipped';
  errorCode?: string;
  message?: string;
}

export interface EnvironmentSSHCheck {
  id: string;
  environmentId: string;
  environmentRevisionId: string;
  status: 'healthy' | 'degraded';
  durationMs: number;
  results: EnvironmentSSHHostCheck[];
  checkedAt: string;
}

export interface EnvironmentConnectivityCheck {
  tcpCheck: EnvironmentHealthCheck;
  sshCheck: EnvironmentSSHCheck;
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
  sshCheck?: EnvironmentSSHCheck;
  archivedAt?: string;
  updatedAt?: string;
}

export interface EnvironmentRevisionDeletionImpact {
  environmentId: string;
  environmentName: string;
  revisionId: string;
  revision: number;
  current: boolean;
  archived: boolean;
  runCount: number;
  imageBuildCount: number;
  healthCheckCount: number;
  sshCheckCount: number;
  canDelete: boolean;
  blockers: Array<{ code: string; message: string }>;
}

export interface EnvironmentLifecycle {
  revisionCount: number;
  runCount: number;
  activeRunCount: number;
  imageBuildCount: number;
  activeImageBuildCount: number;
  installationCount: number;
  archived: boolean;
  canDelete: boolean;
  canArchive: boolean;
}

export interface EnvironmentExportDocument {
  formatVersion: 'clusterforge-environment/v1';
  exportedAt: string;
  containsCredentialReferences: boolean;
  source: { environmentId: string; environmentName: string; revisionId: string; revision: number };
  snapshot: {
    facts: Record<string, unknown>;
    hosts: EnvironmentHost[];
    parameters: Record<string, unknown>;
    variables: Record<string, string>;
    credentialRefs: Array<{ name: string; kind: CredentialRef['type']; reference?: string; configured?: boolean }>;
  };
}

export interface EnvironmentImportPlan {
  planDigest: string;
  targetKind: 'new' | 'existing';
  targetEnvironmentId?: string;
  targetCurrentRevisionId?: string;
  nextRevision: number;
  hostCount: number;
  variableCount: number;
  parameterCount: number;
  credentialRefCount: number;
  changes: string[];
  warnings: string[];
}

export interface EnvironmentParameterField {
  valueKey: string;
  label: string;
  description: string;
  type: ParameterType;
  required: boolean;
  suggestedValue?: unknown;
  enum?: unknown[];
  minLength?: number;
  bindings: Array<{ componentId: string; componentName: string; releaseId: string; version: string; parameterName: string; lineId?: string; lineName?: string; canViewContract?: boolean }>;
}

export interface ScenarioParameterOverview {
  revisionId: string;
  editable: boolean;
  components: Array<{
    componentId: string;
    componentName: string;
    releases: Array<{
      releaseId: string;
      version: string;
      nodes: Array<{
        nodeId: string;
        label: string;
        action: ActionDefinition['type'];
        hostGroup: string;
        parameters: ParameterDefinition[];
        parameterValues: Record<string, unknown>;
        completed: number;
        required: number;
        errors: string[];
        staleKeys: string[];
      }>;
    }>;
  }>;
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
  kind: 'component_draft' | 'component_review' | 'scenario_revision' | 'environment' | 'run' | 'upstream_impact' | 'catalog_backup';
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
 hostGroup?:string;
 parentAction?: string;
 sourceType?: string; stage?: string; acceptanceJobId?: string; scenarioRevisionId?: string;
 phase?: string;
 parentActionId?: string;
 actionId?: string;
 sourceNodeId?: string;
 role?: string;
 contentDigest?: string;
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
  decidedBy?: string;
  decision?: string;
  reason?: string;
}

export interface DeliveryRequirement {
  id: string;
  kind: 'artifact' | 'image';
  name: string;
  identity: string;
  source: string;
  target?: string;
  sourceReadable: boolean;
  targetPresent: boolean;
  transferAvailable: boolean;
  componentName: string;
}

export interface DeliveryDecision {
  requirementId: string;
  mode: 'direct' | 'transfer';
  decidedBy?: string;
  decidedAt?: string;
}

export interface DeliveryResult {
  requirementId: string;
  mode: 'direct' | 'transfer';
  status: 'pending' | 'direct' | 'reused_target' | 'transferred' | 'failed';
  actualLocation?: string;
  message?: string;
  completedAt?: string;
}

export interface RunWaitingObservation {
  host: string;
  task: string;
  stepId: string;
  waiting: { object?: string; expected?: string; observed?: string; attempt?: number; deadline?: string };
}

export interface RunActivity {
  runId: string;
  status: RunStatus;
  logs: Array<{ id: number; runId: string; stepId: string; stream: string; message: string; createdAt: string }>;
  nextAfterId: number;
  hasMore: boolean;
  waitingObservations: RunWaitingObservation[];
  archived: boolean;
}

export interface Run {
 executionMode?: ScenarioExecutionMode; sourceRevisionId?: string; baselineRunId?: string;
 jobDigest?: string;
 exitCode?: number;
  archive?: import('./runRetention').ArchiveInfo;
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
  retryOfRunId?: string;
  retryRootRunId?: string;
  retryAttempt?: number;
  retryStartStep?: number;
  queuePosition?: number;
  progress?: number;
  steps?: RunStep[];
  approval?: Approval;
  purposeCounts?: {components:number;finalVerification:number;acceptance:number;total:number};

  resolvedParametersByNode?: Record<string, Record<string, ResolvedParameter>>;
  backups?: RunBackup[];
  artifactTransfers?: Array<{ alias: string; sourceUrl: string; targetStation: string; relativePath: string; sha256: string }>;
  imageTransfers?: Array<{ sourceRegistry: string; targetRegistry: string; sourceDigest: string; targetDigest: string }>;
  deliveryRequirements?: DeliveryRequirement[];
  deliveryDecisions?: DeliveryDecision[];
  deliveryResults?: DeliveryResult[];
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface RunRetryPlan {
  sourceRunId: string;
  retryRootRunId: string;
  environmentRevisionId: string;
  startStep: number;
  skippedSteps: number;
  remainingSteps: ComponentTestPlanStep[];
  requiresApproval: boolean;
  planDigest: string;
}

export type ComponentTestMode = 'install_verify' | 'rollback' | 'evolution_round_trip';

export type RollbackVerification =
  | { kind: 'target_release'; releaseId: string }
  | { kind: 'rollback_only' };

export interface ComponentTestRequest {
 actionId?: string;
  environmentId: string;
  mode: ComponentTestMode;
  rollbackVerification?: RollbackVerification;
  expectedPlanDigest?: string;
}

export interface ComponentTestPlanStep {
  rollbackSourceActionId?: string;
 name?: string;
 sourceType?: string;
 stage?: string;
 phase?: string;
 parentActionId?: string;
 actionId?: string;
 nodeId?: string;
  order: number;
  componentId: string;
  componentName: string;
  releaseId: string;
  releaseVersion: string;
  action: ActionDefinition['type'] | 'acceptance';
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
  deliveryRequirements: DeliveryRequirement[];
}

export interface EnvironmentRollbackPlan extends ComponentTestPlan {
  nodes: string[];
  environmentName: string;
  sources: Array<{ runId: string; kind: NonNullable<Run['kind']>; scenarioRevisionId?: string; componentCount: number }>;
  componentCount: number;
  nodeCount: number;
}

export interface CatalogRecoveryPoint {
  ref: string;
  commit: string;
  createdAt: string;
}

export interface CatalogRepositoryStatus {
  enabled: boolean;
  configured: boolean;
  reasonCode?: string;
  reason?: string;
  path?: string;
  branch: string;
  allowedRoot: string;
  recoveryPoints: CatalogRecoveryPoint[];
  restoreTargetKnown: boolean;
  targetCatalogEmpty: boolean;
  targetComponentCount: number;
  targetScenarioCount: number;
  behind: boolean;
  currentGeneration: number;
  backedUpGeneration: number;
  lastSuccessfulAt?: string;
  lastError?: string;
  lastErrorAt?: string;
}

export interface CatalogBackupResult {
  backupId: string;
  status: 'success';
  reason: string;
  createdAt: string;
  completedAt: string;
  publicationGeneration: number;
  gitCommit: string;
  gitTag: string;
}

export interface CatalogRestorePlan {
  gitCommit: string;
  schemaContract: string;
  catalogSha256: string;
  counts: Record<string, number>;
  playbookCount: number;
  targetComponentCount: number;
  targetScenarioCount: number;
  planDigest: string;
}

export interface CatalogRecoveryResult extends CatalogRestorePlan {
  restored: boolean;
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
  changeKind: 'new_line' | 'evolution' | 'deprecation';
  lineId?: string;
  lineName?: string;
  fromReleaseId?: string;
  toReleaseId?: string;
  componentOwners: Array<{ id: string; name: string }>;
  scenarioOwners: Array<{ id: string; name: string }>;
  scenarios: Array<{ id: string; name: string }>;
  paths: string[][];
  scenarioRunCount: number;
}

export const ROLE_LABELS: Record<Role, string> = {
  component_owner: '组件 Owner',
  scenario_owner: '集群 Owner',
  environment_owner: '环境 Owner',
  platform_admin: '平台 Owner',
};

export const STATUS_LABELS: Record<string, string> = {
  draft: '草稿',
  released: '已发布',
  deprecated: '已废弃',
  abandoned: '已放弃',
  testing: '测试中',
  test_passed: '测试通过',
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

export interface RunDiagnostic {
  stepId?: string;
  logId?: number;
  component?: string;
  phase?: string;
  task?: string;
  host?: string;
  message: string;
  exitCode?: number;
  stdout?: string;
  stderr?: string;
  raw?: string;
  source: 'event' | 'text' | 'run';
  truncated?: boolean;
}
export interface RunDiagnostics {
  runId: string;
  status: Run['status'];
  capturedAt: string;
  lastLogId: number;
  logCount: number;
  items: RunDiagnostic[];
  omitted?: number;
}
