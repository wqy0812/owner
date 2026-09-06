import { expect, test } from '@playwright/test';

const users = {
  'component-owner-a': { id: 'component-owner-a', name: 'Component Owner A', role: 'component_owner' },
  'component-owner-b': { id: 'component-owner-b', name: 'Component Owner B', role: 'component_owner' },
  'scenario-owner-a': { id: 'scenario-owner-a', name: 'Scenario Owner', role: 'scenario_owner' },
  'environment-owner-a': { id: 'environment-owner-a', name: 'Environment Owner', role: 'environment_owner' },
} as const;

test('identity switch changes owner-specific component controls', async ({ page }) => {
  let current = users['component-owner-a'];
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let data: unknown = [];
    if (path.endsWith('/session/users')) data = Object.values(users);
    else if (path.endsWith('/session/me')) data = current;
    else if (path.endsWith('/session/switch')) {
      const id = (request.postDataJSON() as { userId: keyof typeof users }).userId;
      current = users[id];
      data = current;
    } else if (path.endsWith('/components')) data = [{ id: 'containerd', name: 'containerd', ownerId: 'component-owner-a', layer: 'runtime_state', tags: ['runtime'], latestRelease: { id: 'containerd-2', componentId: 'containerd', lineId: 'line-containerd', lineName: 'containerd', compatibility: 'not_applicable', version: 'v2.1.1', status: 'released', review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] }, parameters: [], dependencies: [], actions: [], artifacts: [], images: [] }, releases: [] }];
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(Array.isArray(data) ? { items: data } : { data }) });
  });

  await page.goto('/components');
  await expect(page.getByRole('heading', { name: '组件中心' })).toBeVisible();
  await expect(page.getByRole('button', { name: '新建组件' })).toBeVisible();
  await page.getByLabel('切换演示身份').selectOption('environment-owner-a');
  await expect(page.getByRole('button', { name: '新建组件' })).toHaveCount(0);
  await page.getByRole('link', { name: '集群', exact: true }).click();
  await expect(page.getByRole('heading', { name: '场景编排' })).toBeVisible();
});

test('scenario parameters stay in the graph and test submission binds the execution preview', async ({ page }) => {
  let submitted: unknown;
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let data: unknown = [];
    if (path.endsWith('/session/users')) data = Object.values(users);
    else if (path.endsWith('/session/me')) data = users['scenario-owner-a'];
    else if (path.endsWith('/components')) data = [{
      id: 'test-runtime', name: 'Test Runtime', ownerId: 'component-owner-a', layer: 'runtime_state', tags: ['runtime'],
      latestRelease: {
        id: 'test-runtime-1.1', componentId: 'test-runtime', lineId: 'line-runtime', lineName: 'Runtime', compatibility: 'not_applicable', version: 'v1.1.0', status: 'released', review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] },
        parameters: [{ name: 'runtime_version', description: 'rollback target', type: 'string', required: true, visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner', suggestedValue: '1.0.0' }], dependencies: [], artifacts: [], images: [],
        actions: [{ kind: 'install', playbook: 'upgrade.yml', hostGroup: 'test_nodes' }, { kind: 'verify', playbook: 'verify.yml', hostGroup: 'test_nodes' }, { kind: 'rollback', playbook: 'rollback.yml', hostGroup: 'test_nodes' }],
      },
      releases: [{
        id: 'test-runtime-1.1', componentId: 'test-runtime', lineId: 'line-runtime', lineName: 'Runtime', compatibility: 'not_applicable', version: 'v1.1.0', status: 'released', review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] },
        parameters: [{ name: 'runtime_version', description: 'rollback target', type: 'string', required: true, visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner', suggestedValue: '1.0.0' }], dependencies: [], artifacts: [], images: [],
        actions: [{ kind: 'install', playbook: 'upgrade.yml', hostGroup: 'test_nodes' }, { kind: 'verify', playbook: 'verify.yml', hostGroup: 'test_nodes' }, { kind: 'rollback', playbook: 'rollback.yml', hostGroup: 'test_nodes' }],
      }],
    }];
    else if (path.endsWith('/scenarios')) data = [{
      id: 'safe-upgrade', slug: 'safe-upgrade', name: 'Safe upgrade', ownerId: 'scenario-owner-a', currentRevisionId: 'safe-upgrade-r2',
      currentRevision: { id: 'safe-upgrade-r2', scenarioId: 'safe-upgrade', revision: 2, state: 'draft', nodes: [{ id: 'runtime', type: 'component', position: { x: 80, y: 80 }, data: { label: 'Runtime node', componentId: 'test-runtime', releaseId: 'test-runtime-1.1', action: 'install', hostGroup: 'test_nodes', parameterValues: { runtime_version: '1.0.0' }, dependencySources: {} } }], edges: [] },
      revisions: [{ id: 'safe-upgrade-r2', scenarioId: 'safe-upgrade', revision: 2, state: 'draft', nodes: [{ id: 'runtime', type: 'component', position: { x: 80, y: 80 }, data: { label: 'Runtime node', componentId: 'test-runtime', releaseId: 'test-runtime-1.1', action: 'install', hostGroup: 'test_nodes', parameterValues: { runtime_version: '1.0.0' }, dependencySources: {} } }], edges: [] }],
    }];
    else if (path.endsWith('/environments')) data = [{ id: 'test', name: 'Test Environment', ownerId: 'environment-owner-a', currentRevision: { id: 'test-r1', environmentId: 'test', revision: 1, facts: {}, hosts: [], parameters: {}, variables: {}, credentialRefs: [] } }];
    else if (path.endsWith('/platform-option-categories')) data = [{id:'host-groups',key:'hostGroup',label:'主机组',kind:'host_group',options:[{id:'test-group',categoryId:'host-groups',value:'test_nodes',label:'测试节点'}]}];
    else if (path.endsWith('/execution-preparations')) data = {id:'prepared-ui-plan',status:'succeeded',input:request.postDataJSON(),output:{checks:[],plan:{scenarioRevisionId:'safe-upgrade-r2',environmentId:'test',executionMode:'install',planDigest:'locked-ui-plan',ready:true,operations:[],steps:[],issues:[]}}};
    else if (path.endsWith('/execution-plan')) data = { scenarioRevisionId: 'safe-upgrade-r2', environmentId: 'test', executionMode: 'install', planDigest: 'locked-ui-plan', ready: true, operations: [], steps: [], issues: [] };
    else if (path.endsWith('/test-runs')) {
      submitted = request.postDataJSON();
      data = { id: 'run-1', status: 'queued', environmentId: 'test' };
    }
    await route.fulfill({ status: path.endsWith('/test-runs') ? 202 : 200, contentType: 'application/json', body: JSON.stringify(Array.isArray(data) ? { items: data } : { data }) });
  });

  await page.goto('/scenarios');
  await page.getByRole('button', { name: '参数总览' }).click();
  const overview = page.locator('.scenario-parameter-overview');
  await expect(overview.getByText('Test Runtime')).toBeVisible();
  await expect(overview.getByText('v1.1.0 · test-runtime-1.1')).toBeVisible();
  await expect(overview.getByText('install · 测试节点')).toBeVisible();
  await expect(overview.getByText('1/1 已完成')).toBeVisible();
  await expect(overview.getByRole('textbox', { name: 'runtime_version 的值' })).toHaveValue('1.0.0');
  await overview.getByRole('button', { name: '定位节点' }).click();
  await page.getByText('Runtime node').click();
  await expect(page.getByRole('combobox', { name: '目标集群动作' })).toHaveValue('install');
  await expect(page.getByRole('combobox', { name: '目标集群动作' })).toBeDisabled();
  await expect(page.getByRole('option', { name: 'uninstall' })).toHaveCount(0);
  await page.getByRole('button', { name: '环境测试' }).click();
  await page.getByRole('combobox', { name: '场景执行环境' }).selectOption('test');
  await expect(page.getByRole('button', { name: '提交安装测试' })).toBeDisabled();
  await page.getByRole('button', { name: '预览执行计划' }).click();
  await page.getByRole('button', { name: '提交安装测试' }).click();
  await expect.poll(() => submitted).toEqual({ environmentId: 'test', executionMode: 'install', expectedPlanDigest: 'locked-ui-plan', idempotencyKey: expect.any(String) });
});

test('scenario dependencies are generated and an ambiguous exact Release source is selected explicitly', async ({ page }) => {
  let savedGraph: any;
  const runtimeRelease = {
    id: 'runtime-r1', componentId: 'runtime-component', lineId: 'runtime-line', lineName: 'Runtime', compatibility: 'not_applicable', version: '1.0.0', status: 'released',
    review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] }, parameters: [], dependencies: [], artifacts: [], images: [],
    actions: [{ kind: 'install', playbook: 'runtime.yml', hostGroup: 'runtime_nodes' }],
  };
  const controlRelease = {
    id: 'control-r1', componentId: 'control-component', lineId: 'control-line', lineName: 'Control', compatibility: 'not_applicable', version: '1.0.0', status: 'released',
    review: { status: 'approved' }, readiness: { status: 'ready', blockers: [] }, parameters: [], artifacts: [], images: [],
    dependencies: [{ id: 'dep-runtime', upstreamComponentId: 'runtime-component', upstreamComponentName: 'Runtime', upstreamReleaseId: runtimeRelease.id, upstreamVersion: runtimeRelease.version, purpose: 'CRI', parameterMappings: [] }],
    actions: [{ kind: 'install', playbook: 'control.yml', hostGroup: 'control_plane' }],
  };
  const revision = {
    id: 'auto-r1', scenarioId: 'auto', revision: 1, state: 'draft', edges: [],
    nodes: [
      { id: 'runtime-a', type: 'component', position: { x: 80, y: 80 }, data: { label: 'Runtime A', componentId: 'runtime-component', releaseId: runtimeRelease.id, action: 'install', hostGroup: 'runtime_nodes', parameterValues: {}, dependencySources: {} } },
      { id: 'runtime-b', type: 'component', position: { x: 80, y: 300 }, data: { label: 'Runtime B', componentId: 'runtime-component', releaseId: runtimeRelease.id, action: 'install', hostGroup: 'runtime_nodes', parameterValues: {}, dependencySources: {} } },
      { id: 'control', type: 'component', position: { x: 420, y: 180 }, data: { label: 'Control', componentId: 'control-component', releaseId: controlRelease.id, action: 'install', hostGroup: 'control_plane', parameterValues: {}, dependencySources: {} } },
    ],
  };
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let data: unknown = [];
    if (path.endsWith('/session/users')) data = Object.values(users);
    else if (path.endsWith('/session/me')) data = users['scenario-owner-a'];
    else if (path.endsWith('/components')) data = [
      { id: 'runtime-component', name: 'Runtime', ownerId: 'component-owner-a', layer: 'runtime_state', tags: [], latestRelease: runtimeRelease, releases: [runtimeRelease] },
      { id: 'control-component', name: 'Control', ownerId: 'component-owner-a', layer: 'orchestration_core', tags: [], latestRelease: controlRelease, releases: [controlRelease] },
    ];
    else if (path.endsWith('/scenarios')) data = [{ id: 'auto', slug: 'auto', name: 'Automatic dependencies', ownerId: 'scenario-owner-a', currentRevisionId: revision.id, currentRevision: revision, revisions: [revision] }];
    else if (path.endsWith('/scenario-revisions/auto-r1/graph') && request.method() === 'PUT') {
      savedGraph = request.postDataJSON().graph;
      // The API enriches submitted nodes with the locked component identity.
      Object.assign(revision, { nodes: savedGraph.nodes.map((node: any) => ({ ...node, data: { ...revision.nodes.find(item => item.id === node.id)?.data, ...node.data } })), edges: savedGraph.edges, revisionDigest: `saved-${Date.now()}` });
      data = revision;
    } else if (path.endsWith('/workbench')) data = { generatedAt: '', role: 'scenario_owner', summary: {}, assets: {}, items: [] };
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(Array.isArray(data) ? { items: data } : { data }) });
  });

  await page.goto('/scenarios');
  await expect(page.getByText('1 项依赖待处理')).toBeVisible();
  await page.getByText('Control', { exact: true }).last().click();
  await page.getByRole('combobox', { name: '来源节点' }).selectOption('runtime-b');
  await expect(page.locator('.scenario-edge--dependency')).toHaveCount(1);
  await expect(page.getByText('1 项依赖待处理')).toHaveCount(0);
  await page.getByRole('button', { name: '保存草稿' }).click();
  await expect.poll(() => savedGraph?.nodes?.find((node: any) => node.id === 'control')?.data?.dependencySources).toEqual({ 'dep-runtime': 'runtime-b' });
  await expect.poll(() => savedGraph?.edges).toEqual([expect.objectContaining({ source: 'runtime-b', target: 'control', kind: 'dependency', dependencyId: 'dep-runtime' })]);
  const warning = page.locator('.scenario-order-warning');
  await expect(warning).toContainText('执行顺序尚未确定');
  await expect(warning.getByRole('button')).toHaveCount(2);
  await expect(page.getByRole('button', { name: '环境测试', exact: true })).toBeDisabled();
  await expect(page.locator('.scenario-node--order-pending')).toHaveCount(2);
  await page.getByRole('button', { name: '节点表', exact: true }).click();
  await warning.getByRole('button').first().click();
  await expect(page.locator('.react-flow__node[data-id="runtime-a"]')).toHaveClass(/selected/);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole('button', { name: 'Fit View', exact: true }).click();
  const source = page.locator('.react-flow__node[data-id="runtime-a"] .react-flow__handle.source');
  const target = page.locator('.react-flow__node[data-id="runtime-b"] .react-flow__handle.target');
  await source.scrollIntoViewIfNeeded();
  const from = (await source.boundingBox())!;
  const to = (await target.boundingBox())!;
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 15 });
  await page.mouse.up();
  await expect(warning).toHaveCount(0);
  await expect(page.locator('.scenario-node--order-pending')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '环境测试', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: '保存草稿' }).click();
  await expect.poll(() => savedGraph?.edges).toEqual(expect.arrayContaining([expect.objectContaining({ source: 'runtime-a', target: 'runtime-b', kind: 'sequence' })]));
  await expect(page.getByRole('button', { name: '环境测试', exact: true })).toBeEnabled();

});
