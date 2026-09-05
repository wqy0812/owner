import { expect, test } from '@playwright/test';

test.skip(!process.env.LIVE_API, 'set LIVE_API=1 against a running Go API');

test('component page shows upstream parameter lineage and visibility editor', async ({ page }) => {
  const usersResponse = await page.request.get('/api/v1/session/users');
  expect(usersResponse.ok()).toBeTruthy();
  const usersPayload = await usersResponse.json() as { items?: Array<{ id: string; role: string }> };
  let selected: { componentId: string; componentName: string } | undefined;
  for (const user of usersPayload.items ?? []) {
    if (user.role !== 'component_owner') continue;
    const switched = await page.request.post('/api/v1/session/switch', { data: { userId: user.id } });
    expect(switched.ok()).toBeTruthy();
    const componentsResponse = await page.request.get('/api/v1/components');
    expect(componentsResponse.ok()).toBeTruthy();
    const componentsPayload = await componentsResponse.json() as {
      items?: Array<{
        id: string;
        name: string;
        ownerId: string;
        releases?: Array<{ dependencies?: Array<{ parameterMappings?: unknown[] }> }>;
      }>;
    };
    for (const item of componentsPayload.items ?? []) {
      if (item.ownerId !== user.id) continue;
      const detailResponse = await page.request.get(`/api/v1/components/${encodeURIComponent(item.id)}`);
      expect(detailResponse.ok()).toBeTruthy();
      const payload = await detailResponse.json();
      const component = payload.data ?? payload;
      if (component.releases?.some((release: { dependencies?: Array<{ parameterMappings?: unknown[] }> }) =>
        release.dependencies?.some((dependency) => (dependency.parameterMappings?.length ?? 0) > 0))) {
        selected = { componentId: item.id, componentName: item.name };
        break;
      }
    }
    if (selected) break;
  }
  expect(selected, 'seeded catalog must expose a component dependency contract').toBeTruthy();

  await page.goto(`/components?selected=${encodeURIComponent(selected!.componentId)}`);
  await expect(page.getByRole('heading', { name: '组件中心' })).toBeVisible();
  await page.getByRole('button', { name: '全部展开' }).click();
  await page.getByRole('button', { name: new RegExp(`^${selected!.componentName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`) }).click();
  expect(await page.getByText(/本组件参数 .* 来自 .* 的公开参数/).count()).toBeGreaterThan(0);
  await expect(page.getByText('各版本参数来源')).toBeVisible();

  const editContract = page.getByRole('button', { name: /创建 Draft 编辑合同|选择 Draft 编辑合同|编辑依赖和参数/ });
  await expect(editContract).toBeVisible();
  await editContract.click();
  if (await page.getByRole('dialog', { name: /创建 Draft 编辑依赖和参数/ }).count()) {
    // Catalog examples have no executable workspace; exercise parameter authoring
    // with a new line instead of cloning their incomplete Playbook manifests.
    await page.getByRole('combobox', { name: '创建方式' }).selectOption('new_line');
    await page.getByRole('textbox', { name: '发布线名称' }).fill(`UI contract ${Date.now()}`);
    await page.getByPlaceholder('v1.1.0').fill(`1.0.0-ui-${Date.now()}`);
    await page.locator('textarea[name="notes"]').fill('验证页面内依赖和参数编辑');
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('button', { name: '创建 Draft', exact: true }).click();
  }

  await expect(page.getByRole('button', { name: '新增依赖' })).toBeVisible();
  await expect(page.getByRole('button', { name: '新增参数' })).toBeVisible();
  if (await page.getByRole('button', { name: /编辑参数/ }).count() === 0) {
    await page.getByRole('button', { name: '新增参数' }).click();
  } else {
    await expect(page.getByRole('radio', { name: /内部/ })).toHaveCount(0);
    await page.getByRole('button', { name: /编辑参数/ }).first().click();
  }
  await expect(page.getByRole('radio', { name: /内部/ }).first()).toBeVisible();
  await expect(page.getByRole('radio', { name: /公开/ }).first()).toBeVisible();
  await page.getByRole('radio', { name: /公开/ }).first().click();
  await expect(page.getByRole('radio', { name: /公开/ }).first()).toBeChecked();
  const valueProvider = page.getByRole('combobox', { name: '值的负责人' }).last();
  await valueProvider.click();
  await valueProvider.selectOption('scenario_owner');
  await expect(valueProvider).toHaveValue('scenario_owner');
  await expect(page.getByRole('button', { name: '保存依赖和参数' })).toBeVisible();
});
