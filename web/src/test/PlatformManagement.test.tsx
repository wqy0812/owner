import { beforeEach, describe, expect, it, vi } from 'vitest';
import { user as userEvent } from './interactions';
import { screen, waitFor, within } from './render';

import { PlatformManagementPage } from '../pages/PlatformManagementPage';
import { admin, installFetch, json, platformOptionCategories } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(PlatformManagementPage, '/platform-management');

beforeEach(() => { installFetch(); });

describe("PlatformManagement", () => {
  it('keeps platform directory maintenance separate from contract review', async () => {
    installFetch({ initialUser: admin });
    renderApp('/platform-management');
    await screen.findByRole('heading', { name: '平台管理' });
    expect(screen.queryByRole('columnheader', { name: '技术值' })).not.toBeInTheDocument();
    expect(screen.queryByText('arm64', { selector: 'code' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Component Release 合同审核' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('tab', { name: '主机组' }));
    const hostGroupCard = screen.getAllByRole('heading', { name: '主机组' }).find(item => item.closest('section')?.querySelector('.category-actions'))!.closest('section')!;
    const hostGroupActions = hostGroupCard.querySelector<HTMLElement>('.category-actions')!;
    expect(within(hostGroupActions).getByRole('button', { name: '彻底删除' })).toBeDisabled();
  });

  it('creates an environment dimension category with its required flag', async () => {
    const fetchMock = installFetch({ initialUser: admin });
    renderApp('/platform-management');
    await screen.findByRole('heading', { name: '平台管理' });
    await userEvent.click(screen.getByRole('button', { name: '新增类别' }));
    await userEvent.type(screen.getByPlaceholderText('例如 CPU 厂商'), 'CPU 厂商');
    const requiredToggle = screen.getByRole('checkbox', { name: '环境录入时必填' });
    expect(requiredToggle).not.toBeChecked();
    await userEvent.click(requiredToggle);
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '新增类别' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => {
      if (!String(input).endsWith('/platform-option-categories') || init?.method !== 'POST') return false;
      return JSON.parse(String(init.body)).label === 'CPU 厂商' && JSON.parse(String(init.body)).environmentRequired === true;
    })).toBe(true));

  });

  it('adds an architecture option from its scoped dialog', async () => {
    const fetchMock = installFetch({ initialUser: admin });
    renderApp('/platform-management');
    await screen.findByRole('heading', { name: '平台管理' });
    const architectureCard = screen.getByRole('heading', { name: '架构' }).closest('section')!;
    expect(within(architectureCard).queryByRole('textbox')).not.toBeInTheDocument();
    await userEvent.click(within(architectureCard).getByRole('button', { name: '新增选项' }));
    const optionDialog = screen.getByRole('dialog', { name: '新增选项 · 架构' });
    await userEvent.type(within(optionDialog).getByRole('textbox', { name: '为架构新增选项' }), 'RISC-V');
    await userEvent.click(within(optionDialog).getByRole('button', { name: '新增选项' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/platform-option-categories/architecture/options') && init?.method === 'POST' && String(init.body).includes('RISC-V'))).toBe(true));

  });

  it('creates an environment variable field without retired global parameters', async () => {
    const fetchMock = installFetch({ initialUser: admin });
    renderApp('/platform-management');
    await screen.findByRole('heading', { name: '平台管理' });
    expect(screen.queryByRole('heading', { name: '全局环境参数字段' })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/environment-parameter-definitions'))).toBe(false);

    await userEvent.click(screen.getByRole('tab', { name: '环境变量' }));
    const variablePanel = screen.getByRole('heading', { name: '环境变量字段' }).closest('section')!;
    await userEvent.type(within(variablePanel).getByRole('textbox', { name: '变量名' }), 'http_proxy');
    await userEvent.type(within(variablePanel).getByRole('textbox', { name: '显示名' }), 'HTTP 代理');
    await userEvent.click(within(variablePanel).getByRole('button', { name: '新增变量字段' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/environment-variable-definitions') && init?.method === 'POST' && String(init.body).includes('HTTP_PROXY'))).toBe(true));
    expect(screen.queryByRole('button', { name: '新建组件' })).not.toBeInTheDocument();
  });

  it('hides retired global management even when historical definitions exist', async () => {
    const definition = { id: 'field-enabled', key: 'field_enabled', label: '功能开关', description: '环境开关', type: 'boolean', usage: 2, createdBy: admin.id, createdAt: '' };
    installFetch({ initialUser: admin, parameterDefinitions: [definition] });
    renderApp('/platform-management');
    await screen.findByRole('heading', { name: '平台管理' });
    expect(screen.queryByRole('button', { name: '编辑功能开关默认值' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '全局环境参数字段' })).not.toBeInTheDocument();
    expect(within(await (async () => { await userEvent.click(screen.getByRole('tab', { name: '环境变量' })); return screen.getByRole('tabpanel', { name: '环境变量' }); })()).getByRole('heading', { name: '环境变量字段' })).toBeInTheDocument();
  });

  it('groups child dimensions under each parent option and scopes child drafts', async () => {
    const fetchMock = installFetch({ initialUser: admin });
    renderApp('/platform-management');

    const runtimeHeading = await screen.findByRole('heading', { name: '容器运行时' });
    const runtimeCard = runtimeHeading.closest('section')!;
    expect(runtimeCard).toHaveClass('category-card--hierarchical');
    expect(within(runtimeCard).getByText('运行时版本', { selector: '.category-path__child strong' })).toBeInTheDocument();
    expect(within(runtimeCard).queryByRole('combobox')).not.toBeInTheDocument();

    const dockerGroup = within(runtimeCard).getByText('Docker').closest('article')!;
    const containerdGroup = within(runtimeCard).getByText('containerd').closest('article')!;
    expect(within(dockerGroup).getByText('20.10.21')).toBeInTheDocument();
    expect(within(dockerGroup).getByText('20.10.24')).toBeInTheDocument();
    expect(within(containerdGroup).getByText('2.0.10')).toBeInTheDocument();

    expect(within(runtimeCard).queryByRole('textbox')).not.toBeInTheDocument();
    await userEvent.click(within(dockerGroup).getByRole('button', { name: '新增运行时版本' }));
    const dockerInput = within(screen.getByRole('dialog')).getByRole('textbox', { name: '为Docker新增运行时版本' });
    await userEvent.type(dockerInput, '25.0.0');
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '取消' }));
    await userEvent.click(within(containerdGroup).getByRole('button', { name: '新增运行时版本' }));
    const containerdInput = within(screen.getByRole('dialog')).getByRole('textbox', { name: '为containerd新增运行时版本' });
    expect(containerdInput).toHaveValue('');
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '取消' }));
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/platform-option-categories/containerRuntimeVersion/options') && init?.method === 'POST')).toBe(false);
    await userEvent.click(within(dockerGroup).getByRole('button', { name: '新增运行时版本' }));
    expect(within(screen.getByRole('dialog')).getByRole('textbox')).toHaveValue('25.0.0');
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '新增运行时版本' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => {
      if (!String(input).endsWith('/platform-option-categories/containerRuntimeVersion/options') || init?.method !== 'POST') return false;
      const body = JSON.parse(String(init.body));
      return body.label === '25.0.0' && body.parentOptionId === 'runtime-docker';
    })).toBe(true));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    const dockerHeader = dockerGroup.querySelector<HTMLElement>('.hierarchy-parent__header')!;
    expect(within(dockerHeader).getByRole('button', { name: '退役' })).toBeDisabled();
    expect(within(dockerHeader).getByRole('button', { name: '彻底删除' })).toBeDisabled();
  });

  it('renames platform option labels inline without exposing technical values', async () => {
    installFetch({ initialUser: admin });
    const baseFetch = globalThis.fetch;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      if (url.endsWith('/platform-option-categories/architecture') && init?.method === 'PATCH') {
        return json({ ...platformOptionCategories[0], label: body.label });
      }
      if (url.endsWith('/platform-options/amd64') && init?.method === 'PATCH') {
        return json({ ...platformOptionCategories[0].options[0], label: body.label });
      }
      return baseFetch(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderApp('/platform-management');

    await userEvent.dblClick(await screen.findByRole('heading', { name: '架构' }));
    const categoryInput = screen.getByRole('textbox', { name: '更名类别 架构' });
    await userEvent.clear(categoryInput);
    await userEvent.type(categoryInput, '处理器架构{Enter}');
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/platform-option-categories/architecture') && init?.method === 'PATCH' && String(init.body).includes('处理器架构'))).toBe(true));

    await userEvent.dblClick(screen.getByText('x86/amd64'));
    const optionInput = screen.getByRole('textbox', { name: '更名选项 x86/amd64' });
    await userEvent.clear(optionInput);
    await userEvent.type(optionInput, 'x86_64');
    await userEvent.click(screen.getByRole('heading', { name: '平台管理' }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/platform-options/amd64') && init?.method === 'PATCH' && String(init.body).includes('x86_64'))).toBe(true));
    expect(screen.queryByRole('columnheader', { name: '技术值' })).not.toBeInTheDocument();
  });


});
