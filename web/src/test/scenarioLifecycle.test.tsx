import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { ScenarioCreateModal } from '../components/ScenarioCreateModal';
import { ScenarioExecutionPanel } from '../components/ScenarioExecutionPanel';
import { ScenarioAcceptanceEditor } from '../components/ScenarioAcceptanceEditor';
import { changedReleaseParameterIssues, changedReleaseDependencyIssues, moveAcceptanceJob, newAcceptanceJob, replaceScenarioNodeRelease } from '../pages/scenarioLifecycle';
import type { ComponentRelease, Environment, Scenario, ScenarioAcceptance, ScenarioExecutionPreview, ScenarioNode, ScenarioRevision } from '../types/domain';

const { notify, signalRefresh } = vi.hoisted(() => ({ notify: vi.fn(), signalRefresh: vi.fn() }));
vi.mock('../context/AppContext', () => ({ useApp: () => ({ user: {id:"scenario-owner",role:"scenario_owner"}, notify, signalRefresh, platformOptionCategories: [{ kind: 'host_group', options: [{ id: 'control', value: 'control', label: '控制节点' }] }] }), displayError: (error: unknown) => String(error) }));
beforeEach(() => { vi.restoreAllMocks(); sessionStorage.clear(); notify.mockClear(); signalRefresh.mockClear();
 vi.spyOn(api,'createPreparation').mockImplementation(async request => ({id:`preparation-${request.idempotencyKey}`,status:'succeeded',input:request,output:{checks:[],plan:await api.previewScenarioExecution(request.subjectId,request.scenario as never)}}));
 vi.spyOn(api,'preparationPlan').mockImplementation((_kind,value)=>value as ScenarioExecutionPreview);
});
afterEach(() => vi.unstubAllGlobals());
const revision: ScenarioRevision = { id: 'revision-new', scenarioId: 'scenario', revision: 2, state: 'draft', nodes: [{ id: 'n', type: 'component', position: { x: 0, y: 0 }, data: { label: 'component', componentId: 'component', releaseId: 'target' } }], edges: [], revisionDigest: 'revision-digest', sourceRevisionId: 'revision-old', sourceRunId: 'formal-source' };
const environment: Environment = { id: 'env', name: '隔离环境', ownerId: 'env-owner' };
const targetRelease: ComponentRelease = { id: 'target', componentId: 'component', lineId: 'line', lineName: 'line', version: '2', compatibility: 'compatible', state: 'released', readiness: { status: 'ready', blockers: [] }, review: { status: 'approved' }, parameters: [
  { name: 'retained', type: 'string', description: 'retained', visibility: 'public', modifiable: true, valueProvider: 'scenario_owner' },
  { name: 'changed', type: 'integer', description: 'changed', visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner' },
] };
const node: ScenarioNode = { id: 'same-node', type: 'component', position: { x: 0, y: 0 }, data: { label: 'component', componentId: 'component', releaseId: 'old', parameterValues: { retained: 'keep', changed: 'invalid', removed: 'review' }, dependencySources: { 'old-dependency': 'upstream' } } };

describe('scenario lifecycle editor', () => {
  it('creates acceptance IDs on HTTP origins without crypto.randomUUID', () => {
    vi.stubGlobal('crypto', { getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto) });
    const jobs = [newAcceptanceJob(0), newAcceptanceJob(1)];
    expect(jobs[0].id).toMatch(/^acceptance-[a-f0-9-]{36}$/);
    expect(jobs[0].id).not.toBe(jobs[1].id);
  });
  it('retains values and explicit sources when replacing a Release and reports incompatible values', () => {
    const replaced = replaceScenarioNodeRelease(node, targetRelease);
    expect(replaced.releaseId).toBe('target'); expect(replaced.action).toBe('install');
    expect(replaced.parameterValues).toEqual(node.data.parameterValues);
    expect(replaced.dependencySources).toEqual(node.data.dependencySources);
    expect(changedReleaseParameterIssues(node, targetRelease)).toEqual(['changed 必须是 integer', '失效参数 removed 已不属于目标版本的场景填写合同']);
    expect(() => replaceScenarioNodeRelease(node, { ...targetRelease, componentId: 'different' })).toThrow('同一组件');
  });
  it('moves acceptance jobs without changing their source identity', () => {
    const jobs = [newAcceptanceJob(0), newAcceptanceJob(1)]; const moved = moveAcceptanceJob(jobs, 1, -1);
    expect(moved.map(job => job.id)).toEqual([jobs[1].id, jobs[0].id]); expect(jobs[0].name).toBe('业务验收 1');
    expect(moveAcceptanceJob(jobs, 0, -1)).toBe(jobs);
  });
  it('identifies old and target dependency mappings for explicit migration review', () => {
    const dependency = { componentId: 'upstream', releaseId: 'upstream-r1', parameterMappings: [{ upstreamParameter: 'address', targetParameter: 'endpoint' }] };
    expect(changedReleaseDependencyIssues({ ...targetRelease, dependencies: [dependency] }, { ...targetRelease, dependencies: [{ ...dependency, releaseId: 'upstream-r2' }] })).toEqual([
      '原依赖需迁移：upstream · upstream-r1；映射 address → endpoint', '目标依赖：upstream · upstream-r2；映射 address → endpoint',
    ]);
    expect(changedReleaseDependencyIssues({ ...targetRelease, dependencies: [dependency] }, { ...targetRelease, dependencies: [dependency] })).toEqual([]);
  });
  it('allows the Owner to preview baseline verification for an unpublished test deployment without submitting a test', async () => {
    const preview = vi.spyOn(api, 'previewScenarioExecution').mockResolvedValue({ scenarioRevisionId: revision.id, environmentId: 'env', executionMode: 'baseline_verify', planDigest: 'baseline-plan', operations: [], steps: [], issues: [], ready: true });
    const run = vi.spyOn(api, 'runScenario').mockResolvedValue({ id: 'verification' } as never);
    const test = vi.spyOn(api, 'testScenario');
    render(<ScenarioExecutionPanel revision={revision} environments={[environment]} editable canLaunch onSaved={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('场景执行方式'), { target: { value: 'baseline_verify' } });
    fireEvent.change(screen.getByLabelText('场景执行环境'), { target: { value: 'env' } });
    fireEvent.click(screen.getByRole('button', { name: '预览执行计划' })); await screen.findByText('计划已就绪');
    expect(preview).toHaveBeenCalledWith(revision.id, { environmentId: 'env', executionMode: 'baseline_verify', testOnly: false });
    fireEvent.click(screen.getByRole('button', { name: '开始基线复核' }));
    await waitFor(() => expect(run).toHaveBeenCalledWith(revision.id, 'env', expect.objectContaining({ executionMode: 'baseline_verify', expectedPlanDigest: 'baseline-plan' })));
    expect(test).not.toHaveBeenCalled();
  });
  it('requires a branch preview again after target metadata changes and sends the locked digest', async () => {
    const source: Scenario = { id: 'source', name: '另一 Owner 场景', slug: 'source', ownerId: 'other', revisions: [{ ...revision, id: 'released', revision: 4, state: 'released' }, { ...revision, id: 'draft', state: 'draft' }] };
    const preview = vi.spyOn(api, 'previewScenarioFork').mockResolvedValue({ sourceScenarioId: 'source', sourceRevisionId: 'released', sourceRevision: 4, sourceDigest: 'source-digest', nodeCount: 2, acceptanceJobCount: 1, planDigest: 'fork-plan' });
    const create = vi.spyOn(api, 'forkScenario').mockResolvedValue({ ...source, id: 'fork' });
    render(<ScenarioCreateModal scenarios={[source]} initialSourceRevisionId="released" onClose={vi.fn()} onDone={vi.fn()} />);
    expect(screen.queryByRole('option', { name: /r2/ })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: '场景名称' }), { target: { value: '分支' } }); fireEvent.change(screen.getByRole('textbox', { name: '标识' }), { target: { value: 'branch' } });
    if (!screen.getByRole('checkbox', { name: /确认以上适配范围/ }).hasAttribute('checked')) fireEvent.click(screen.getByRole('checkbox', { name: /确认以上适配范围/ }));
    fireEvent.click(screen.getByRole('button', { name: '预览分支' })); await screen.findByText('分支预览 · 来源 r4');
    fireEvent.change(screen.getByRole('textbox', { name: '场景名称' }), { target: { value: '独立分支' } });
    expect(screen.queryByRole('button', { name: '确认创建分支' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '预览分支' })); await screen.findByText('分支预览 · 来源 r4');
    fireEvent.click(screen.getByRole('button', { name: '确认创建分支' }));
    await waitFor(() => expect(create).toHaveBeenCalledWith({ sourceRevisionId: 'released', name: '独立分支', slug: 'branch', description: '', environmentConstraints: {}, expectedPlanDigest: 'fork-plan' })); expect(preview).toHaveBeenCalledTimes(2);
  });
  it('requires a current preview and submits the selected mode with digest and an idempotency key', async () => {
    vi.stubGlobal('crypto', { getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto) });
    const plan: ScenarioExecutionPreview = { scenarioRevisionId: revision.id, environmentId: 'env', executionMode: 'upgrade', planDigest: 'upgrade-plan', operations: [], steps: [], issues: [], ready: true };
    vi.spyOn(api, 'previewScenarioExecution').mockResolvedValue(plan); const start = vi.spyOn(api, 'testScenario').mockResolvedValue({ id: 'run' } as never);
    render(<ScenarioExecutionPanel revision={revision} environments={[environment]} editable canLaunch initialMode="upgrade" onSaved={vi.fn()} />);
    expect(screen.getByRole('button', { name: '提交升级测试' })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('场景执行环境'), { target: { value: 'env' } }); fireEvent.click(screen.getByRole('button', { name: '预览执行计划' })); await screen.findByText('计划已就绪');
    fireEvent.click(screen.getByRole('button', { name: '提交升级测试' }));
    await waitFor(() => expect(start).toHaveBeenCalledWith(revision.id, 'env', expect.objectContaining({ executionMode: 'upgrade', expectedPlanDigest: 'upgrade-plan', idempotencyKey: expect.stringMatching(/^[a-f0-9-]{36}$/) })));
    expect(screen.getByRole('button', { name: '提交升级测试' })).toBeDisabled();
  });
  it('blocks execution when preview reports a baseline mismatch', async () => {
    vi.spyOn(api, 'previewScenarioExecution').mockResolvedValue({ scenarioRevisionId: revision.id, environmentId: 'env', executionMode: 'upgrade', planDigest: '', operations: [], steps: [], issues: [{ code: 'baseline', message: '环境实际版本与来源不一致' }], ready: false });
    render(<ScenarioExecutionPanel revision={revision} environments={[environment]} editable canLaunch initialMode="upgrade" onSaved={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('场景执行环境'), { target: { value: 'env' } }); fireEvent.click(screen.getByRole('button', { name: '预览执行计划' })); await screen.findByText('环境实际版本与来源不一致');
    expect(screen.getByRole('button', { name: '提交升级测试' })).toBeDisabled();
  });
  it('invalidates the test preview after a version is published', async () => {
    vi.spyOn(api, 'previewScenarioExecution').mockResolvedValue({ scenarioRevisionId: revision.id, environmentId: 'env', executionMode: 'upgrade', planDigest: 'test-plan', operations: [], steps: [], issues: [], ready: true });
    const props = { revision, environments: [environment], editable: false, canLaunch: true, initialMode: 'upgrade' as const, onSaved: vi.fn() };
    const view = render(<ScenarioExecutionPanel {...props} />);
    fireEvent.change(screen.getByLabelText('场景执行环境'), { target: { value: 'env' } });
    fireEvent.click(screen.getByRole('button', { name: '预览执行计划' })); await screen.findByText('计划已就绪');
    expect(screen.getByRole('button', { name: '提交升级测试' })).toBeEnabled();
    view.rerender(<ScenarioExecutionPanel {...props} revision={{ ...revision, state: 'released' }} />);
    expect(screen.getByRole('button', { name: '提交正式升级' })).toBeDisabled();
    expect(screen.queryByText('计划已就绪')).not.toBeInTheDocument();
  });
  it('saves acceptance files against both the opened file SHA and revision identity', async () => {
    const file = { releaseId: revision.id, path: 'templates/check.j2', sha256: 'opened-file', sizeBytes: 4, mediaType: 'text/plain', editable: true, content: 'old' };
    const definition: ScenarioAcceptance = { revisionId: revision.id, revisionDigest: 'revision-digest', editable: true, jobs: [], parameters: [], values: {}, bindings: [], workspace: { root: 'scenario/', treeSha256: 'opened-tree', files: [file] } };
    vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(definition); vi.spyOn(api, 'scenarioAcceptanceFile').mockResolvedValue(file);
    const save = vi.spyOn(api, 'saveScenarioAcceptanceFile').mockResolvedValue({ ...file, sha256: 'new-file', content: 'new' });
    render(<ScenarioAcceptanceEditor revisionId={revision.id} editable nodes={[]} components={[]} environments={[]} onSaved={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'templates/check.j2' })); const editor = await screen.findByLabelText('验收文件在线编辑器');
    fireEvent.change(editor, { target: { value: 'new' } }); fireEvent.click(screen.getByRole('button', { name: '保存验收文件' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(revision.id, { path: file.path, content: 'new', expectedSha256: 'opened-file', expectedTreeSha256: 'opened-tree', expectedRevisionDigest: 'revision-digest' }));
  });
});
