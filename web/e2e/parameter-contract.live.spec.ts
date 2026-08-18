import { expect, test } from '@playwright/test';

test.skip(!process.env.LIVE_API, 'set LIVE_API=1 against a running Go API');

test('component page shows upstream parameter lineage and visibility editor', async ({ page }) => {
  await page.goto('/components');
  await expect(page.getByRole('heading', { name: '组件中心' })).toBeVisible();
  await page.getByLabel('切换演示身份').selectOption('component-bob');
  await page.getByRole('button', { name: /^kube-proxy/ }).click();
  await expect(page.getByText(/本组件参数 kubeRoot 来自 kubelet .*的公开参数 kubeInstallRoot/)).toHaveCount(3);
  await expect(page.getByText('各版本参数来源')).toBeVisible();

  const editVisibility = page.getByRole('button', { name: '编辑参数可见性' });
  if (await editVisibility.count()) {
    await editVisibility.click();
  } else {
    await page.getByRole('button', { name: '创建版本并设置可见性' }).click();
    await page.getByPlaceholder('v1.1.0').fill(`1.17.5-ui-${Date.now()}`);
    await page.locator('textarea[name="notes"]').fill('验证公开内部可见性编辑入口');
    await page.getByRole('button', { name: '创建 Draft' }).click();
  }

  await expect(page.getByRole('dialog', { name: /配置 Draft/ })).toBeVisible();
  if (await page.getByRole('radio', { name: /内部/ }).count() === 0) {
    await page.getByRole('button', { name: '新增参数' }).click();
  }
  await expect(page.getByRole('radio', { name: /内部/ }).first()).toBeVisible();
  await expect(page.getByRole('radio', { name: /公开/ }).first()).toBeVisible();
  await page.getByRole('radio', { name: /公开/ }).first().click();
  await expect(page.getByRole('radio', { name: /公开/ }).first()).toBeChecked();
});
