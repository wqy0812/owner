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
    } else if (path.endsWith('/components')) data = [{ id: 'containerd', name: 'containerd', ownerId: 'component-owner-a', layer: 'runtime_state', tags: ['runtime'], latestRelease: { id: 'containerd-2', componentId: 'containerd', version: 'v2.1.1', status: 'released', readiness: { status: 'ready', blockers: [] } } }];
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(Array.isArray(data) ? { items: data } : { data }) });
  });

  await page.goto('/components');
  await expect(page.getByRole('heading', { name: '组件中心' })).toBeVisible();
  await expect(page.getByRole('button', { name: '新建组件' })).toBeVisible();
  await page.getByLabel('切换演示身份').selectOption('environment-owner-a');
  await expect(page.getByRole('button', { name: '新建组件' })).toHaveCount(0);
  await page.getByRole('link', { name: '场景' }).click();
  await expect(page.getByRole('heading', { name: '场景编排' })).toBeVisible();
});

test('scenario rollback and declared run input are submitted to the API', async ({ page }) => {
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
        id: 'test-runtime-1.1', componentId: 'test-runtime', version: 'v1.1.0', status: 'released', readiness: { status: 'ready', blockers: [] },
        actions: [{ kind: 'upgrade', playbook: 'upgrade.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
      },
      releases: [{
        id: 'test-runtime-1.1', componentId: 'test-runtime', version: 'v1.1.0', status: 'released', readiness: { status: 'ready', blockers: [] },
        actions: [{ kind: 'upgrade', playbook: 'upgrade.yml' }, { kind: 'verify', playbook: 'verify.yml' }, { kind: 'rollback', playbook: 'rollback.yml' }],
      }],
    }];
    else if (path.endsWith('/scenarios')) data = [{
      id: 'safe-upgrade', slug: 'safe-upgrade', name: 'Safe upgrade', ownerId: 'scenario-owner-a', currentRevisionId: 'safe-upgrade-r2',
      currentRevision: { id: 'safe-upgrade-r2', scenarioId: 'safe-upgrade', revision: 2, state: 'draft', nodes: [{ id: 'runtime', type: 'component', position: { x: 80, y: 80 }, data: { label: 'Rollback runtime', componentId: 'test-runtime', releaseId: 'test-runtime-1.1', action: 'rollback', hostGroup: 'test_nodes', runInputs: ['rollback_version'] } }], edges: [] },
      revisions: [{ id: 'safe-upgrade-r2', scenarioId: 'safe-upgrade', revision: 2, state: 'draft', nodes: [{ id: 'runtime', type: 'component', position: { x: 80, y: 80 }, data: { label: 'Rollback runtime', componentId: 'test-runtime', releaseId: 'test-runtime-1.1', action: 'rollback', hostGroup: 'test_nodes', runInputs: ['rollback_version'] } }], edges: [] }],
    }];
    else if (path.endsWith('/environments')) data = [{ id: 'test', name: 'Test Environment', ownerId: 'environment-owner-a', currentRevision: { id: 'test-r1', environmentId: 'test', revision: 1, facts: {}, hosts: [], variables: {}, credentialRefs: [] } }];
    else if (path.endsWith('/test-runs')) {
      submitted = request.postDataJSON();
      data = { id: 'run-1', status: 'queued', environmentId: 'test' };
    }
    await route.fulfill({ status: path.endsWith('/test-runs') ? 202 : 200, contentType: 'application/json', body: JSON.stringify(Array.isArray(data) ? { items: data } : { data }) });
  });

  await page.goto('/scenarios');
  await page.getByText('Rollback runtime').click();
  await expect(page.getByRole('combobox', { name: '生命周期动作' })).toHaveValue('rollback');
  await expect(page.getByRole('option', { name: 'uninstall' })).toHaveCount(0);
  await page.getByRole('button', { name: '环境测试' }).click();
  await page.getByRole('combobox', { name: '共享测试环境' }).selectOption('test');
  await page.getByLabel('运行参数 rollback_version').fill('1.0.0');
  await page.getByRole('button', { name: '开始完整测试' }).click();
  await expect.poll(() => submitted).toEqual({ environmentId: 'test', runInput: { rollback_version: '1.0.0' } });
});
