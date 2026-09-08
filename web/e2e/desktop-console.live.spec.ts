import { expect, test, type Page, type Locator } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { createDraft } from './liveFixtures';

test.skip(!process.env.LIVE_API, 'requires the isolated Go browser fixture');
const screenshotPath = (name: string) => test.info().outputPath(`${name}.png`);
async function identity(page: Page, role: string) {
  const response = await page.request.get('/api/v1/session/users');
  const { items } = await response.json();
  const user = items.find((entry: {role:string})=>entry.role===role);
  expect(user).toBeTruthy();
  expect((await page.request.post('/api/v1/session/switch',{data:{userId:user.id}})).ok()).toBeTruthy();
}
async function capture(page: Page, name: string) {
  await mkdir(path.dirname(screenshotPath(name)),{recursive:true});
  await expect(page.locator('main h1')).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.locator('.ant-tabs-ink-bar').evaluateAll(elements=>Promise.all(elements.flatMap(element=>element.getAnimations()).map(animation=>animation.finished.catch(()=>{}))));
  await page.screenshot({path:screenshotPath(name), animations:'disabled'});
}
async function geometry(dialog: Locator, width: number) {
  await expect(dialog).toBeVisible();
  await expect(dialog).not.toHaveClass(/ant-zoom-(appear|enter)/);
  const box=await dialog.boundingBox();expect(box!.width).toBe(width);expect(box!.y).toBeGreaterThanOrEqual(47);expect(box!.y+box!.height).toBeLessThanOrEqual(1033);
  await expect(dialog.locator('.ant-modal-title h2')).toHaveCSS('font-size','18px');
  const header=await dialog.locator('.ant-modal-header').boundingBox();const footer=await dialog.locator('.ant-modal-footer').boundingBox();
  await dialog.locator('.ant-modal-body').evaluate(element=>element.scrollTop=element.scrollHeight);
  expect(await dialog.locator('.ant-modal-header').boundingBox()).toEqual(header);
  expect(await dialog.locator('.ant-modal-footer').boundingBox()).toEqual(footer);
}
async function option(page:Page,label:string|RegExp){await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({hasText:label}).click();}

test('desktop component dialogs keep actions visible, trap focus and preserve cancelled nested input',async({page})=>{
  await identity(page,'component_owner');await page.goto('/components?selected=browser-consumer');
  await expect(page.getByRole('heading',{name:'browser-consumer',exact:true})).toBeVisible();
  await capture(page,'components-contract');
  await page.getByRole('tab',{name:'版本历史',exact:true}).click();await capture(page,'components-history');
  await page.getByRole('tab',{name:'引用关系',exact:true}).click();await capture(page,'components-usage');
  await page.getByRole('tab',{name:'版本与合同',exact:true}).click();
  const create=page.getByRole('button',{name:'新建组件',exact:true});await create.click();
  let dialog=page.getByRole('dialog',{name:'新建组件',exact:true});await geometry(dialog,720);
  await dialog.getByLabel('组件名称').fill('用于检查长名称与系统字体的组件'.repeat(6));
  await dialog.getByLabel('组件层级').click();await option(page,/L4/);
  await expect(page.locator('.ant-select-dropdown:visible')).toHaveCount(0);
  await capture(page,'create-component');
  await dialog.getByRole('button',{name:'创建组件',exact:true}).focus();
  for(let index=0;index<14;index++){await page.keyboard.press('Tab');expect(await dialog.evaluate(element=>element.contains(document.activeElement))).toBe(true);}
  await page.mouse.click(100,500);await expect(dialog).toBeVisible();
  expect(await dialog.evaluate(element=>element.contains(document.activeElement))).toBe(true);
  await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect(create).toBeFocused();
  await create.click();await expect(page.getByLabel('组件名称')).toHaveValue('');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'Playbook',exact:true}).click();await geometry(page.getByRole('dialog'),1440);await capture(page,'playbook-workspace');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'查看详情',exact:true}).click();await geometry(page.getByRole('dialog'),1040);await capture(page,'release-details');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'新增分支',exact:true}).click();dialog=page.getByRole('dialog');
  await dialog.getByLabel('分支名称').fill('桌面交互预览');await dialog.getByLabel('新版本',{exact:true}).fill('9.0.0');
  const notes='长说明用于检查固定标题、底部操作和正文滚动。'.repeat(60);await dialog.getByLabel('发布说明').fill(notes);
  for(const checkbox of await dialog.getByRole('checkbox',{name:'不限制',exact:true}).all())await checkbox.check();
  await dialog.getByRole('checkbox',{name:'确认以上适配范围，创建后固定。',exact:true}).check();
  await geometry(dialog,1040);await capture(page,'new-branch-long-content');
  const writes:string[]=[];page.on('request',request=>{if(request.method()==='POST'&&request.url().endsWith('/release-drafts'))writes.push(request.url());});
  await dialog.getByRole('button',{name:'创建分支',exact:true}).click();
  const confirm=page.getByRole('dialog',{name:'确认操作',exact:true});await expect(confirm).toBeVisible();await expect(confirm).not.toHaveClass(/ant-zoom-(appear|enter)/);expect((await confirm.boundingBox())!.width).toBe(480);
  await expect(confirm.getByRole('button',{name:'取消',exact:true})).toBeFocused();for(let index=0;index<6;index++){await page.keyboard.press('Tab');expect(await confirm.evaluate(element=>element.contains(document.activeElement))).toBe(true);}await capture(page,'nested-confirmation');
  await page.keyboard.press('Escape');await expect(confirm).toHaveCount(0);await expect(dialog.getByLabel('发布说明')).toHaveValue(notes);expect(writes).toHaveLength(0);
  await expect(dialog.getByRole('button',{name:'创建分支',exact:true})).toBeEnabled();await expect(dialog.getByRole('button',{name:'创建分支',exact:true})).toBeFocused();await page.keyboard.press('Escape');
});

test('populated desktop pages fit the viewport and keep the scenario canvas usable',async({page})=>{
 await identity(page,'component_owner');await page.goto('/');await expect(page.getByRole('heading',{name:'等待我处理',exact:true})).toBeVisible();await capture(page,'workbench');
 await page.goto('/notifications');await expect(page.getByText('上游组件已发布新版本 · 请评估下游影响',{exact:true})).toBeVisible();await capture(page,'notifications');
 await page.goto('/runs?selected=browser-run-1');await expect(page.getByRole('tab',{name:'步骤与日志',exact:true})).toBeVisible();await capture(page,'runs-steps');
 await page.getByRole('tab',{name:'输入与交付',exact:true}).click();await capture(page,'runs-delivery');await page.getByRole('tab',{name:'诊断',exact:true}).click();await capture(page,'runs-diagnostics');
 await identity(page,'environment_owner');await page.goto('/environments?selected=browser-environment');await expect(page.getByRole('tab',{name:'配置',exact:true})).toBeVisible();await capture(page,'environment-config');
 await page.getByRole('tab',{name:'版本历史',exact:true}).click();await capture(page,'environment-history');await page.getByRole('tab',{name:'最近运行',exact:true}).click();await capture(page,'environment-runs');
 await identity(page,'scenario_owner');await page.goto('/scenarios?selected=browser-scenario');await expect(page.locator('.react-flow')).toBeVisible();
 const visible=await page.locator('.react-flow').evaluate(element=>{const box=element.getBoundingClientRect();return Math.min(innerHeight,box.bottom)-Math.max(0,box.top);});expect(visible).toBeGreaterThanOrEqual(600);
 await capture(page,'scenario-canvas');await page.getByRole('button',{name:'收起组件库',exact:true}).click();await page.getByRole('button',{name:'收起节点配置',exact:true}).click();await capture(page,'scenario-expanded-canvas');
 await page.getByRole('tab',{name:'升级作业',exact:true}).click();await capture(page,'scenario-upgrade');await page.getByRole('tab',{name:/业务验收/}).click();await capture(page,'scenario-acceptance');
 await page.getByRole('tab',{name:'目标集群',exact:true}).click();await page.getByRole('button',{name:'环境测试',exact:true}).click();await geometry(page.getByRole('dialog'),1040);await capture(page,'scenario-execution');await page.keyboard.press('Escape');
 await page.goto('/manual');await capture(page,'manual');
 await identity(page,'platform_admin');await page.goto('/platform-management');await expect(page.getByRole('tab',{name:'环境适配维度',exact:true})).toBeVisible();await capture(page,'platform-dimensions');
 await page.getByRole('button',{name:'新增类别',exact:true}).click();await geometry(page.getByRole('dialog'),720);await capture(page,'create-category');await page.keyboard.press('Escape');
 await page.getByRole('tab',{name:'主机组',exact:true}).click();await capture(page,'platform-host-groups');await page.getByRole('tab',{name:'环境变量',exact:true}).click();await capture(page,'platform-variables');
 await identity(page,'environment_owner');await page.goto('/disaster-recovery');await expect(page.getByRole('heading',{name:'灾备目录',exact:true})).toBeVisible();await expect(page.getByText('服务端未启用发布目录灾备',{exact:true})).toBeVisible();await capture(page,'disaster-recovery-disabled');
});


test('desktop editor, upload and environment dialogs retain readable populated controls',async({page})=>{
  await identity(page,'component_owner');await page.goto('/components?selected=browser-consumer');
  await page.getByRole('button',{name:'Playbook',exact:true}).click();
  let dialog=page.getByRole('dialog');await dialog.getByRole('button',{name:'新增第一个动作',exact:true}).click();
  await dialog.getByRole('textbox',{name:'Playbook 在线编辑器',exact:true}).fill('---\n- name: 验证桌面编辑器中的长任务名称和 YAML 缩进\n  hosts: all\n  gather_facts: false\n  tasks:\n    - name: 本地显示测试，不提交或执行\n      ansible.builtin.debug:\n        msg: "桌面字体与代码行滚动检查"\n');
  await geometry(dialog,1440);const editor=dialog.getByRole('textbox',{name:'Playbook 在线编辑器',exact:true});await editor.scrollIntoViewIfNeeded();await editor.evaluate(el=>el.scrollTop=0);await expect(editor).toHaveCSS('background-color','rgb(23, 32, 54)');expect((await editor.boundingBox())!.height).toBeGreaterThanOrEqual(320);await capture(page,'playbook-action-editor');await page.keyboard.press('Escape');
  const discard = page.getByRole('dialog', { name: '确认操作', exact: true });
  await expect(discard).toBeVisible();
  await discard.getByRole('button', { name: '取消', exact: true }).click();
  await expect(discard).toHaveCount(0);
  await expect(editor).toHaveValue(/验证桌面编辑器/);
  await page.keyboard.press('Escape');
  await discard.getByRole('button', { name: '确认', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  for(const [button,name] of [['构建镜像','image-build'],['组件介质','artifacts']] as const){
    await page.getByRole('button',{name:button,exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,1040);await capture(page,name);await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);
  }
  await page.getByRole('button',{name:'组件目录更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'批量导入',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,1040);await capture(page,'component-import');await page.keyboard.press('Escape');
  await identity(page,'environment_owner');await page.goto('/environments?selected=browser-environment');
  await page.getByRole('button',{name:'编辑节点 control-plane-long-hostname-01.example.internal',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,720);await expect(dialog.getByLabel('节点名称')).toHaveValue('control-plane-long-hostname-01.example.internal');await capture(page,'environment-node');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'主机组管理',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,1040);await capture(page,'environment-groups');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'环境目录更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'导入环境',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,1040);await capture(page,'environment-import');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'新建环境',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,720);await capture(page,'environment-create');await page.keyboard.press('Escape');
});


test('configured disaster recovery uses isolated display data and opens guarded dialogs',async({page})=>{
  await identity(page,'environment_owner');
  // Display-only fixture: no repository is created, backed up or restored.
  const status={enabled:true,configured:true,path:'/isolated/desktop/catalog.git',branch:'catalog',allowedRoot:'/isolated/desktop',recoveryPoints:[{ref:'backup/20260907-desktop-acceptance',commit:'a'.repeat(40),createdAt:'2026-09-07T08:00:00Z'}],restoreTargetKnown:true,targetCatalogEmpty:true,targetComponentCount:0,targetScenarioCount:0,lastSuccessfulAt:'2026-09-07T08:00:00Z',currentGeneration:4,backedUpGeneration:4};
  await page.route('**/api/v1/catalog-repository',route=>route.fulfill({json:{data:status}}));
  await page.goto('/disaster-recovery');await expect(page.getByText('已接入私有仓库',{exact:true})).toBeVisible();await capture(page,'disaster-recovery');
  await page.getByRole('button',{name:'创建私有仓库',exact:true}).click();let dialog=page.getByRole('dialog');await geometry(dialog,720);await dialog.getByLabel('服务器仓库路径').fill('desktop-snapshot.git');await capture(page,'disaster-create');await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'从恢复点恢复空库',exact:true}).click();dialog=page.getByRole('dialog');await geometry(dialog,1040);await capture(page,'disaster-restore');await page.keyboard.press('Escape');
});


test('build update dialog prevents old-page work and stays open until refresh',async({page})=>{
  await identity(page,'component_owner');
  await page.route('**/version.json*',route=>route.fulfill({json:{version:'isolated-new-ui-build'}}));
  await page.goto('/components');await expect(page.locator('main h1')).toBeVisible();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));const dialog=page.getByRole('dialog',{name:'请刷新后继续操作',exact:true});await geometry(dialog,720);
  await expect(page.locator('.build-version-guard__content')).toHaveAttribute('inert','');
  await page.keyboard.press('Escape');await expect(dialog).toBeVisible();await expect(dialog.getByRole('button',{name:'刷新使用新版本',exact:true})).toBeEnabled();
  await page.screenshot({path:screenshotPath('build-update'),animations:'disabled'});
});

test('saves an action through the real API, reopens its YAML and preserves local text on conflict', async ({ page }) => {
  await identity(page, 'component_owner');
  const { component, release } = await createDraft(page.request, 'action-browser-acceptance');
  await page.goto(`/components?selected=${component.id}`);
  await page.getByRole('button', { name: 'Playbook', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: /配置 Draft/ });
  await dialog.getByRole('button', { name: '新增第一个动作', exact: true }).click();
  const content = '---\n- name: Persist a valid Role task\n  debug:\n    msg: saved by browser acceptance\n';
  await dialog.getByRole('textbox', { name: 'Playbook 在线编辑器', exact: true }).fill(content);
  const endpoint = `/api/v1/component-releases/${release.id}/playbook`;
  const savedResponse = page.waitForResponse(response => response.url().endsWith(endpoint) && response.request().method() === 'PUT');
  await dialog.getByRole('button', { name: '保存 Playbook', exact: true }).click();
  const response = await savedResponse;
  const payload = await response.json();
  expect(response.ok(), JSON.stringify(payload)).toBeTruthy();
  const { data: saved } = payload;
  const readback = await page.request.get(`${endpoint}?actionId=${encodeURIComponent(saved.action.id)}`);
  expect(readback.ok()).toBeTruthy();
  expect((await readback.json()).data.content).toBe(content);
  await expect(dialog.getByRole('button', { name: '保存 Draft', exact: true })).toBeEnabled();
  await dialog.getByRole('button', { name: '关闭编辑器', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await page.reload();
  await page.getByRole('button', { name: 'Playbook', exact: true }).click();
  dialog = page.getByRole('dialog', { name: /配置 Draft/ });
  const editor = dialog.getByRole('textbox', { name: 'Playbook 在线编辑器', exact: true });
  await expect(editor).toHaveValue(content);

  const workspace = await page.request.get(`/api/v1/component-releases/${release.id}/playbook-workspace`);
  expect(workspace.ok()).toBeTruthy();
  const { data: tree } = await workspace.json();
  const remoteContent = content.replace('saved by browser acceptance', 'updated by another editor');
  const remoteSave = await page.request.put(endpoint, { data: { actionKind: saved.action.kind, action: saved.action, content: remoteContent, expectedSha256: saved.sha256, expectedTreeSha256: tree.treeSha256 } });
  expect(remoteSave.ok()).toBeTruthy();
  const localContent = content.replace('saved by browser acceptance', 'keep these local edits');
  await editor.fill(localContent);
  const conflictResponse = page.waitForResponse(result => result.url().endsWith(endpoint) && result.request().method() === 'PUT');
  await dialog.getByRole('button', { name: '保存 Playbook', exact: true }).click();
  expect((await conflictResponse).status()).toBe(409);
  await expect(page.getByText('保存 Playbook 失败', { exact: true })).toBeVisible();
  await expect(editor).toHaveValue(localContent);
  await expect(dialog.getByRole('button', { name: '保存 Draft', exact: true })).toBeDisabled();
  const finalReadback = await page.request.get(`${endpoint}?actionId=${encodeURIComponent(saved.action.id)}`);
  expect((await finalReadback.json()).data.content).toBe(remoteContent);
});
