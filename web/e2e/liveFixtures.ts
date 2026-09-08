import { expect, type APIRequestContext, type Page } from '@playwright/test';

export async function useIdentity(page: Page, role: string) {
  const response = await page.request.get('/api/v1/session/users');
  expect(response.ok()).toBeTruthy();
  const { items } = await response.json();
  const user = items.find((item: { role: string }) => item.role === role);
  expect(user).toBeTruthy();
  expect((await page.request.post('/api/v1/session/switch', { data: { userId: user.id } })).ok()).toBeTruthy();
}

export async function createDraft(request: APIRequestContext, purpose: string) {
  const response = await request.post('/api/v1/components', { data: { name: purpose, slug: `${purpose}-${Date.now()}`, layer: 'runtime_state', tags: [] } });
  expect(response.ok(), await response.text()).toBeTruthy();
  const { data: component } = await response.json();
  const input = { mode: 'new_line', lineName: 'Browser acceptance', version: '1.0.0', releaseNotes: 'Isolated browser persistence test', riskLevel: 'low', environmentConstraints: {} };
  const preview = await request.post(`/api/v1/components/${component.id}/release-draft-plan`, { data: input });
  expect(preview.ok(), await preview.text()).toBeTruthy();
  const { data: plan } = await preview.json();
  const creation = await request.post(`/api/v1/components/${component.id}/release-drafts`, { data: { ...input, expectedPlanDigest: plan.planDigest } });
  expect(creation.ok(), await creation.text()).toBeTruthy();
  const { data: release } = await creation.json();
  return { component, release };
}

export async function chooseOption(page: Page, name: string, label: string) {
  await page.getByRole('combobox', { name, exact: true }).click();
  await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: label }).click();
}
