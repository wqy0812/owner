import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { AppProvider } from '../context/AppContext';
import { EditReleaseModal } from '../features/components/releases/ReleaseDialogs';
import { executableActionTypes } from '../types/domain';
import { answerConfirm, selectOption } from './antdInteractions';
import { defaultResponse, installReadModelFixtures } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { fireEvent, render, screen, waitFor, within } from './render';

import { ComponentsPage } from '../pages/ComponentsPage';
import { alice, dave, installFetch, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(ComponentsPage, '/components');

beforeEach(() => { installFetch(); });

describe("PlaybookIntegration", () => {
  it('shows released details and Playbook content without Draft edit shortcuts', async () => {
    const released = {
      id: 'release-readonly', componentId: 'component-readonly', version: '1.2.3', status: 'released', riskLevel: 'medium',
      releaseNotes: 'immutable release details', parameters: [], dependencies: [],
      actions: [{ name: 'install', kind: 'install', playbook: 'managed/readonly/install.yml', hostGroup: 'workers', timeoutSeconds: 900, requiredCredentials: ['SSH_KEY'] }],
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/components')) return json([{
        id: 'component-readonly', name: 'Readonly Component', slug: 'readonly-component', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'], latestRelease: released, releases: [released],
      }]);
      if (url.includes('/component-releases/release-readonly/playbook?actionId=action-install')) return json({
        path: 'managed/readonly/install.yml', filename: 'install.yml', content: '---\n- hosts: workers\n  tasks: []\n', sha256: 'abc123',
      });
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    }));

    renderApp('/components');
    expect(await screen.findByRole('button', { name: '查看详情' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增版本' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建 Draft 编辑合同' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /创建 Draft 后编辑/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '编辑直接依赖' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '查看详情' }));
    const dialog = screen.getByRole('dialog', { name: '1.2.3 Release 详情' });
    expect(dialog).toHaveTextContent('immutable release details');
    expect(dialog).toHaveTextContent('SSH_KEY');
    expect(await within(dialog).findByText(/hosts: workers/)).toBeInTheDocument();
  });

  it('loads completed image build logs when opening the new log disclosure', async () => {
    const baseFetch = installFetch();
    const build = {
      id: 'completed-build', releaseId: 'release-containerd-2', requestedBy: alice.id,
      status: 'failed', dockerfileSha256: 'a'.repeat(64), imageTag: 'historical',
      imageRef: 'registry.example.test/components/runtime:historical', createdAt: new Date().toISOString()
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/component-releases/release-containerd-2/image-builds')) return json([build]);
      if (url.endsWith('/image-builds/completed-build')) return json({
        ...build,
        logs: [{ id: 1, buildId: build.id, stream: 'stderr', message: 'Completed build diagnostic retained', createdAt: build.createdAt }]
      });
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/components?selected=component-containerd&release=release-containerd-2');
    await userEvent.click(await screen.findByRole('button', { name: '构建镜像' }));
    await userEvent.click(await screen.findByText('查看构建日志'));
    await waitFor(() => expect(screen.getByLabelText('镜像构建日志')).toHaveTextContent('Completed build diagnostic retained'));
    expect(fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/image-builds/completed-build'))).toHaveLength(1);
  });

  it('locks the selected environment when submitting a Dockerfile image build', async () => {
    const draft = {
      id: 'release-image-draft', componentId: 'component-image', version: '1.0.0-rc1',
      status: 'draft', releaseNotes: 'Image candidate', parameters: [], dependencies: [], actions: [],
    };
    let submitted: FormData | undefined;
    const build = {
      id: 'image-build-test', releaseId: draft.id, environmentId: 'environment-build', environmentRevisionId: 'environment-build-r3',
      requestedBy: alice.id, status: 'queued', dockerfileSha256: 'a'.repeat(64), imageTag: '1.0.0-rc1',
      imageRef: 'registry.example.test:5000/components/image:1.0.0-rc1', createdAt: new Date().toISOString(),
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/image-builds/image-build-test')) return json(build);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.endsWith('/component-releases/release-image-draft/image-builds') && init?.method === 'POST') {
        submitted = init.body as FormData;
        return json(build);
      }
      if (url.endsWith('/component-releases/release-image-draft/image-builds')) return json([]);
      if (url.endsWith('/components')) return json([{
        id: 'component-image', name: 'Image component', slug: 'image', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'], latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/environments')) return json([{
        id: 'environment-build', name: 'Build Environment', ownerId: dave.id,
        currentRevision: { id: 'environment-build-r3', environmentId: 'environment-build', revision: 3, facts: {}, hosts: [], variables: { IMAGE_REGISTRY: 'registry.example.test:5000' }, credentialRefs: [] },
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderApp('/components?selected=component-image');
    await userEvent.click(await screen.findByRole('button', { name: '构建镜像' }));
    expect(await screen.findByText('IMAGE_REGISTRY=registry.example.test:5000')).toBeInTheDocument();
    await userEvent.upload(screen.getByLabelText('Dockerfile').closest('.file-picker')!.querySelector('input[type=file]')!, new File(['FROM scratch\n'], 'Dockerfile', { type: 'text/plain' }));
    const submitButton = screen.getByRole('button', { name: '上传并构建' });
    await waitFor(() => expect(submitButton).toBeEnabled());
    fireEvent.submit(submitButton.closest('form')!);

    await waitFor(() => expect(submitted).toBeDefined());
    expect(submitted?.get('environmentId')).toBe('environment-build');
    expect(submitted?.get('tag')).toBe('1.0.0-rc1');
  });

  it('sends an explicit empty CredentialRef list and keeps it cleared after reopening', async () => {
    let draft = {
      id: 'release-credential-draft', componentId: 'component-credential', version: '1.0.0-rc1',
      status: 'draft' as const, definitionGeneration: 1, releaseNotes: 'Credential draft', parameters: [], dependencies: [],
      actions: [{ id: 'action-install', name: 'install', kind: 'install' as const, playbook: 'managed/credential/tasks/install.yml', requiredCredentials: ['K8S_BOOTSTRAP_TOKEN'] }],
    };
    let submitted: Record<string, unknown> | undefined;
    let atomicAction: Record<string, unknown> | undefined;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/session/me')) return json(alice);
      if (url.includes('/component-releases/release-credential-draft/playbook?actionId=action-install')) return json({ path: 'managed/credential/tasks/install.yml', filename: 'install.yml', content: '---\n- name: Credential check\n  debug: { msg: ready }\n', sha256: 'credential-playbook-sha' });
      if (url.endsWith('/component-releases/release-credential-draft/playbook-workspace')) return json({ root: 'managed/credential/', treeSha256: 'credential-tree-sha', files: [{ releaseId: 'release-credential-draft', path: 'tasks/install.yml', sha256: 'credential-playbook-sha', sizeBytes: 32, mediaType: 'application/yaml', editable: true }] });
      if (url.endsWith('/component-releases/release-credential-draft/playbook') && init?.method === 'PUT') {
        const parsed = JSON.parse(String(init.body)); atomicAction = parsed.action;
        draft = { ...draft, definitionGeneration: draft.definitionGeneration + 1, actions: [{ ...parsed.action, playbook: 'managed/credential/tasks/install.yml' }] };
        return json({ path: 'managed/credential/tasks/install.yml', filename: 'install.yml', content: parsed.content, sha256: 'credential-playbook-sha-2', action: { ...parsed.action, playbook: 'managed/credential/tasks/install.yml' } });
      }
      if (url.endsWith('/component-releases/release-credential-draft') && init?.method === 'PUT') {
        const parsed = JSON.parse(String(init.body)) as Record<string, unknown>;
        submitted = parsed;
        draft = { ...draft, ...parsed, status: 'draft', actions: (parsed.actions as typeof draft.actions).map((action) => ({ ...action, playbook: `managed/credential/tasks/${action.kind}.yml` })) } as typeof draft;
        return json(draft);
      }
      if (url.endsWith('/components')) return json([{
        id: 'component-credential', name: 'Credential component', slug: 'credential', ownerId: alice.id,
        layer: 'runtime_state', tags: ['runtime'],
        latestRelease: draft, releases: [draft],
      }]);
      if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
      return defaultResponse(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderDraftEditor('component-credential');
    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByText('K8S_BOOTSTRAP_TOKEN')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '重新载入' }));
    await screen.findByDisplayValue(/Credential check/);
    await userEvent.click(screen.getByRole('button', { name: '清空全部' }));
    expect(screen.getByText('请先保存未完成的编辑')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
    await waitFor(() => expect(atomicAction?.requiredCredentials).toEqual([]));
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: '保存 Draft' }));

    await waitFor(() => expect(submitted).toBeDefined());
    expect((submitted?.actions as Array<Record<string, unknown>>)[0].requiredCredentials).toEqual([]);
    expect(await screen.findByText(/已清除 1 个 CredentialRef/)).toBeInTheDocument();

    await userEvent.click(await screen.findByRole('button', { name: 'Playbook' }));
    expect(screen.getByText('未声明 CredentialRef')).toBeInTheDocument();
    expect(screen.queryByText('K8S_BOOTSTRAP_TOKEN')).not.toBeInTheDocument();
  });

  it('persists multiple lifecycle actions with distinct managed Playbooks', async () => {
    const fixture = installPlaybookDraft(true);
    await renderDraftEditor('component-docker');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    await screen.findByRole('button', { name: '保存 Draft' });
    for (const kind of ['install', 'check', 'rollback']) expect(screen.getByRole('button', { name: kind })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '保存 Draft' }));
    await waitFor(() => expect(fixture.submittedActions).toHaveLength(3));
    expect(fixture.submittedActions.map(action => action.kind)).toEqual(['install', 'check', 'rollback']);
    expect(fixture.submittedActions.map(action => action.name)).toEqual(['install', 'check', 'rollback']);
    expect(fixture.submittedActions[0].idempotent).toBe(true);
    expect(fixture.submittedActions.every(action => !('playbook' in action))).toBe(true);
    expect(fixture.submittedActions[2]).toMatchObject({ fromReleaseId: 'release-docker-draft', toReleaseId: 'release-docker-previous' });
    await screen.findByText('Draft 配置已保存');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    for (const kind of ['install', 'check', 'rollback']) expect(await screen.findByRole('button', { name: kind })).toBeInTheDocument();
  });

  it('persists retry safety with an edited action and recognizes content restored to its saved value', async () => {
    const fixture = installPlaybookDraft();
    await renderDraftEditor('component-docker');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    await selectOption(await screen.findByRole('combobox', { name: '可安全重试' }), /是 · 已验证/);
    await userEvent.click(screen.getByRole('button', { name: '重新载入' }));
    const editor = await screen.findByDisplayValue(/Install Docker/);
    const original = (editor as HTMLTextAreaElement).value;
    await userEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
    await waitFor(() => expect(fixture.persistedAction.idempotent).toBe(true));
    expect(fixture.saveRequests[0]).toMatchObject({
      actionKind: 'install', action: { id: 'action-install', kind: 'install', idempotent: true },
      content: original, expectedSha256: 'existing-playbook-sha256', expectedTreeSha256: 'workspace-tree-sha256',
    });
    fireEvent.change(editor, { target: { value: 'temporary local edit' } });
    expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
    fireEvent.change(editor, { target: { value: original } });
    expect(screen.getByText('内容已保存')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
  });

  it('confirms before discarding an unsaved Playbook to add another action', async () => {
    installPlaybookDraft();
    await renderDraftEditor('component-docker');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    await userEvent.click(await screen.findByRole('button', { name: '重新载入' }));
    const editor = await screen.findByDisplayValue(/Install Docker/);
    fireEvent.change(editor, { target: { value: 'locally changed install' } });
    await userEvent.click(screen.getByRole('button', { name: '新增动作' }));
    expect(await screen.findByText('当前 Playbook 有未保存内容，确认放弃并新增动作？')).toBeInTheDocument();
    await answerConfirm(false);
    expect(editor).toHaveValue('locally changed install');
    await userEvent.click(screen.getByRole('button', { name: '新增动作' }));
    await answerConfirm();
    expect(within(screen.getByRole('navigation', { name: 'Ansible 动作' })).getAllByRole('button', { name: 'rollback' }).at(-1)).toHaveClass('active');
  });

  it.each(['check', 'rollback'] as const)('saves a new %s action with its own managed Playbook', async kind => {
    const fixture = installPlaybookDraft();
    await renderDraftEditor('component-docker');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    await userEvent.click(await screen.findByRole('button', { name: '新增动作' }));
    await selectOption(screen.getByRole('combobox', { name: '动作类型' }), new RegExp(kind));
    expect(screen.getByText(kind === 'check' ? 'tasks/checks/待保存.yml' : 'tasks/rollback.yml')).toBeInTheDocument();
    if (kind === 'rollback') {
      await selectOption(screen.getByRole('combobox', { name: '来源 Release' }), /26.1.0/);
      await selectOption(screen.getByRole('combobox', { name: '目标 Release' }), /25.0.0/);
    }
    fireEvent.change(screen.getByRole('textbox', { name: 'Playbook 在线编辑器' }), { target: { value: `---\n- name: ${kind} Docker\n  debug: { msg: ready }\n` } });
    await userEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
    await waitFor(() => expect(fixture.persistedAction.kind).toBe(kind));
    expect(fixture.saveRequests).toHaveLength(1);
    expect(fixture.saveRequests[0]).toMatchObject({
      actionKind: kind, action: { kind }, content: `---\n- name: ${kind} Docker\n  debug: { msg: ready }\n`,
      expectedSha256: '', expectedTreeSha256: 'workspace-tree-sha256',
    });
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
    if (kind === 'rollback') expect(fixture.persistedAction).toMatchObject({ fromReleaseId: 'release-docker-draft', toReleaseId: 'release-docker-previous' });
  });

  it('removes a persisted lifecycle action only after confirmation', async () => {
    const fixture = installPlaybookDraft(true);
    await renderDraftEditor('component-docker');
    await userEvent.click(screen.getByRole('button', { name: 'Playbook' }));
    await userEvent.click(await screen.findByRole('button', { name: 'check' }));
    await userEvent.click(screen.getByRole('button', { name: '移除动作' }));
    await answerConfirm();
    await waitFor(() => expect(fixture.deletedActionKind).toBe('check'));
    expect(await screen.findByText('Action 已删除')).toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'Ansible 动作' })).queryByRole('button', { name: 'check' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '关闭编辑器' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

describe("action capability conversion", () => {
  it('exposes upgrade once when install is idempotent', () => {
    expect(executableActionTypes([{ type: 'install', playbook: 'install.yml', idempotent: true }])).toEqual(['install', 'upgrade']);
    expect(executableActionTypes([{ type: 'install', playbook: 'install.yml' }])).toEqual(['install']);
    expect(executableActionTypes([
      { type: 'install', playbook: 'install.yml', idempotent: true },
      { type: 'upgrade', playbook: 'upgrade.yml' },
    ])).toEqual(['install', 'upgrade']);
  });
});

function installPlaybookDraft(allActions = false) {
  const previousRelease = {
    id: 'release-docker-previous', componentId: 'component-docker', version: '25.0.0',
    status: 'released', releaseNotes: 'Previous Docker Runtime', parameters: [], dependencies: [], actions: [],
  };
  let draft = {
    id: 'release-docker-draft', componentId: 'component-docker', version: '26.1.0',
    status: 'draft', definitionGeneration: 1, releaseNotes: 'Docker Runtime draft', parameters: [], dependencies: [],
    actions: [{ name: 'install', kind: 'install', playbook: 'managed/docker/release-docker-draft/tasks/install.yml', timeoutSeconds: 1800, riskLevel: 'low' }],
  };
  let persistedAction: Record<string, unknown> = {};
  let submittedActions: Array<Record<string, unknown>> = [];
  const saveRequests: Array<Record<string, unknown>> = [];
  let deletedActionKind = '';
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/session/me')) return json(alice);
    if (url.includes('/component-releases/release-docker-draft/playbook?actionId=action-install') && (!init?.method || init.method === 'GET')) {
      return json({
        path: 'managed/docker/release-docker-draft/tasks/install.yml',
        filename: 'install.yml',
        content: '---\n- name: Install Docker\n  debug: { msg: installed }\n',
        sha256: 'existing-playbook-sha256',
      });
    }
    if (url.endsWith('/component-releases/release-docker-draft/playbook-workspace')) {
      return json({
        root: 'managed/docker/release-docker-draft/',
        treeSha256: 'workspace-tree-sha256',
        files: [{ releaseId: 'release-docker-draft', path: 'tasks/install.yml', sha256: 'existing-playbook-sha256', sizeBytes: 64, mediaType: 'application/yaml', editable: true }],
      });
    }
    if (url.endsWith('/component-releases/release-docker-draft/playbook') && init?.method === 'PUT') {
      const body = JSON.parse(String(init.body));
      const id = body.action.id || `action-${body.actionKind}`;
      const entry = body.actionKind === 'check' ? `tasks/checks/${id}.yml` : `tasks/${body.actionKind}.yml`;
      const savedAction = { ...body.action, id, playbook: `managed/docker/release-docker-draft/${entry}` };
      draft = { ...draft, definitionGeneration: draft.definitionGeneration + 1, actions: [...draft.actions.filter(item => item.kind !== body.actionKind), savedAction] };
      persistedAction = body.action;
      saveRequests.push(body);
      return json({
        path: savedAction.playbook,
        filename: `${body.actionKind}.yml`,
        content: body.content,
        sha256: 'playbook-sha256',
        action: savedAction,
      });
    }
    if (url.includes('/component-releases/release-docker-draft/playbook?actionId=') && (!init?.method || init.method === 'GET')) {
      const id = new URL(url, 'http://localhost').searchParams.get('actionId');
      const action = draft.actions.find(item => `action-${item.kind}` === id);
      if (action) return json({ path: action.playbook, filename: `${action.kind}.yml`, content: `---\n- name: ${action.kind} Docker\n  debug: { msg: test }\n`, sha256: `sha-${id}` });
    }
    if (url.includes('/component-releases/release-docker-draft/playbook?actionId=') && init?.method === 'DELETE') {
      deletedActionKind = new URL(url, 'http://localhost').searchParams.get('actionId')?.replace('action-', '') ?? '';
      draft = { ...draft, definitionGeneration: draft.definitionGeneration + 1, actions: draft.actions.filter((action) => action.kind !== deletedActionKind) };
      return json({ root: 'managed/docker/release-docker-draft/', treeSha256: 'workspace-tree-after-delete', files: [] });
    }
    if (url.endsWith('/component-releases/release-docker-draft') && init?.method === 'PUT') {
      const body = JSON.parse(String(init.body));
      submittedActions = body.actions;
      draft = { ...draft, ...body, status: 'draft', actions: body.actions.map((action: Record<string, unknown>) => ({ ...action, playbook: `managed/docker/release-docker-draft/${action.kind}.yml` })) };
      return json(draft);
    }
    if (url.endsWith('/components')) return json([{
      id: 'component-docker', name: 'Docker Runtime', slug: 'docker', ownerId: alice.id,
      layer: 'runtime_state', tags: ['runtime'],
      latestRelease: draft, releases: [draft, previousRelease],
    }]);
    if (url.endsWith('/scenarios') || url.endsWith('/environments') || url.endsWith('/runs') || url.endsWith('/notifications')) return json([]);
    return defaultResponse(input, init);
  });
  if (allActions) draft = {
    ...draft, actions: [
      { id: 'action-install', name: 'install', kind: 'install', playbook: 'managed/docker/release-docker-draft/tasks/install.yml', idempotent: true },
      { id: 'action-check', name: 'check', kind: 'check', playbook: 'managed/docker/release-docker-draft/tasks/checks/check.yml' },
      { id: 'action-rollback', name: 'rollback', kind: 'rollback', playbook: 'managed/docker/release-docker-draft/tasks/rollback.yml', fromReleaseId: draft.id, toReleaseId: previousRelease.id },
    ].map(action => ({ timeoutSeconds: 1800, riskLevel: 'low', ...action }))
  };
  vi.stubGlobal('fetch', fetchMock);

  return { saveRequests, get submittedActions() { return submittedActions; }, get deletedActionKind() { return deletedActionKind; }, get persistedAction() { return persistedAction; } };
}

/** Exercise the real editor and persistence, with no unrelated catalog rendering. */
async function renderDraftEditor(componentId: string) {
  installReadModelFixtures();
  const initial = await api.component(componentId);
  function Editor() {
    const [component, setComponent] = useState(initial);
    const [open, setOpen] = useState(false);
    return <><button onClick={async () => { setComponent(await api.component(componentId)); setOpen(true); }}>Playbook</button>
      {open && <EditReleaseModal release={component.releases![0]} releases={component.releases!} onClose={() => setOpen(false)} onDone={() => setOpen(false)} />}</>;
  }
  const view = render(<AppProvider><Editor /></AppProvider>);
  await screen.findByRole('button', { name: 'Playbook' });
  return view;
}
