import { expect, test } from '@playwright/test';
import { chooseOption, createDraft, useIdentity } from './liveFixtures';
import type { Environment, EnvironmentExportDocument } from '../src/types/domain';

test.skip(!process.env.LIVE_API, 'requires the isolated Go browser fixture');

test('parameter contract retains numeric enums and the environment value binding after save and reload', async ({ page }) => {
  await useIdentity(page, 'component_owner');
  const { component, release } = await createDraft(page.request, 'parameter-persistence');
  await page.goto(`/components?selected=${component.id}`);
  await page.getByRole('button', { name: '编辑参数合同', exact: true }).click();
  await page.getByRole('button', { name: '新增参数', exact: true }).click();
  await page.getByRole('textbox', { name: '参数名称', exact: true }).fill('listen_port');
  await page.getByRole('textbox', { name: '参数说明', exact: true }).fill('业务监听端口');
  await chooseOption(page, '参数类型', 'integer');
  await chooseOption(page, '值的负责人', '环境 Owner 填写');
  await page.getByRole('radio', { name: '公开', exact: true }).check();
  await page.getByRole('spinbutton', { name: 'listen_port 的值', exact: true }).first().fill('8080');
  await page.getByRole('spinbutton', { name: 'listen_port 的值', exact: true }).last().fill('9090');
  await page.getByRole('textbox', { name: '枚举', exact: true }).fill('[8080,9090]');
  const response = page.waitForResponse(response => response.url().endsWith(`/component-releases/${release.id}/contract`) && response.request().method() === 'PATCH');
  await page.getByRole('button', { name: '保存参数合同', exact: true }).click();
  const saved = await response;
  expect(saved.ok(), await saved.text()).toBeTruthy();
  const detail = await page.request.get(`/api/v1/components/${component.id}`);
  const { data } = await detail.json();
  expect(data.releases.find((item: { id: string }) => item.id === release.id).parameters).toEqual([
    expect.objectContaining({ name: 'listen_port', type: 'integer', enum: [8080, 9090], suggestedValue: 8080, testValue: 9090, valueProvider: 'environment_owner', environmentBinding: { kind: 'private' }, visibility: 'public', modifiable: true }),
  ]);
  await expect(page.getByRole('button', { name: '编辑参数合同', exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: '编辑参数合同', exact: true }).click();
  await page.getByRole('button', { name: '编辑参数 listen_port', exact: true }).click();
  await expect(page.getByRole('textbox', { name: '枚举', exact: true })).toHaveValue('[8080,9090]');
  await expect(page.getByRole('combobox', { name: 'listen_port 的值', exact: true }).first().locator('..')).toContainText('8080');
  await expect(page.getByRole('combobox', { name: '值的负责人', exact: true }).locator('..')).toContainText('环境 Owner 填写');
});

test('environment import preserves cancellation, rejects stale previews and records the committed revision', async ({ page }) => {
  await useIdentity(page, 'environment_owner');
  const environments = async (): Promise<Environment[]> => {
    const response = await page.request.get('/api/v1/environments');
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).items;
  };
  const source = (await environments()).find(item => item.id === 'browser-environment')!;
  const exported = await page.request.post(`/api/v1/environments/${source.id}/revisions/${source.currentRevision!.id}/export`, { data: { includeCredentialReferences: true } });
  expect(exported.ok(), await exported.text()).toBeTruthy();
  const document: EnvironmentExportDocument = await exported.json();
  const name = `import-persistence-${Date.now()}`;
  const input = { document, target: { kind: 'new', name }, changeReason: 'Browser fixture', confirmCredentialReferences: true };
  const preview = await page.request.post('/api/v1/environment-imports/plan', { data: input });
  expect(preview.ok(), await preview.text()).toBeTruthy();
  const created = await page.request.post('/api/v1/environment-imports', { data: { ...input, expectedPlanDigest: (await preview.json()).data.planDigest } });
  expect(created.ok(), await created.text()).toBeTruthy();
  const environment: Environment = (await created.json()).data;
  const endpoint = `/api/v1/environments/${environment.id}`;
  const read = async () => (await environments()).find(item => item.id === environment.id)!;
  const writes: unknown[] = [];
  page.on('request', request => { if (request.method() === 'POST' && request.url().endsWith('/environment-imports')) writes.push(request.postDataJSON()); });
  await page.goto(`/environments?selected=${environment.id}`);
  async function openImport() {
    await page.getByRole('button', { name: '环境目录更多操作', exact: true }).click();
    await page.getByRole('menuitem', { name: '导入环境', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '导入环境版本', exact: true });
    await dialog.getByRole('textbox', { name: '环境导出 JSON', exact: true }).fill(JSON.stringify(document));
    await dialog.getByRole('textbox', { name: '变更原因', exact: true }).fill('Browser checked import');
    const confirmReferences = dialog.getByRole('checkbox', { name: /我确认复用文件中的 CredentialRef/ });
    if (await confirmReferences.count()) await confirmReferences.check();
    await dialog.getByRole('button', { name: '预览差异', exact: true }).click();
    await expect(dialog.getByRole('button', { name: '确认创建版本', exact: true })).toBeEnabled();
    return dialog;
  }
  let dialog = await openImport();
  await dialog.getByRole('button', { name: '取消', exact: true }).click();
  expect(writes).toHaveLength(0);
  expect((await read()).currentRevision!.id).toBe(environment.currentRevision!.id);
  dialog = await openImport();
  const changed = await page.request.put(`${endpoint}/inventory`, { data: { hosts: document.snapshot.hosts.map((host, index) => index === 0 ? { ...host, user: 'concurrent-editor' } : host), changeReason: 'Concurrent editor' } });
  expect(changed.ok(), await changed.text()).toBeTruthy();
  const concurrent: Environment = (await changed.json()).data;
  expect(concurrent.currentRevision!.revision).toBe(2);
  const conflictResponse = page.waitForResponse(response => response.url().endsWith('/environment-imports') && response.request().method() === 'POST');
  await dialog.getByRole('button', { name: '确认创建版本', exact: true }).click();
  expect((await conflictResponse).status()).toBe(409);
  await expect(page.getByText('环境导入失败', { exact: true })).toBeVisible();
  expect((await read()).currentRevision!.id).toBe(concurrent.currentRevision!.id);
  await expect(dialog.getByRole('button', { name: '确认创建版本', exact: true })).toBeDisabled();
  await dialog.getByRole('button', { name: '预览差异', exact: true }).click();
  await expect(dialog.getByRole('button', { name: '确认创建版本', exact: true })).toBeEnabled();
  const savedResponse = page.waitForResponse(response => response.url().endsWith('/environment-imports') && response.request().method() === 'POST');
  await dialog.getByRole('button', { name: '确认创建版本', exact: true }).click();
  expect((await savedResponse).ok()).toBeTruthy();
  await expect(dialog).toHaveCount(0);
  const persisted = await read();
  expect(persisted.currentRevision!.revision).toBe(3);
  expect(persisted.currentRevision!.hosts).toEqual(document.snapshot.hosts);
  expect(persisted.currentRevision!.variables).toEqual(document.snapshot.variables);
  expect(persisted.revisions!.map((revision: { revision: number }) => revision.revision)).toEqual([3, 2, 1]);
  await page.reload();
  await page.getByRole('tab', { name: '版本历史', exact: true }).click();
  await expect(page.getByText('Browser checked import', { exact: true })).toBeVisible();
});
