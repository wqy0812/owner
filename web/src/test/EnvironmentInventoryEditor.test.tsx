import { useState } from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { EnvironmentInventoryEditor } from '../components/EnvironmentInventoryEditor';
import type { EnvironmentHost, PlatformOption } from '../types/domain';

const options: PlatformOption[] = ['group-a', 'group-b', 'retired'].map((value, index) => ({
  id: value, categoryId: 'host-group', value, label: value, sortOrder: index,
  createdBy: 'test-owner', createdAt: '', usage: { componentReleases: 0, scenarioRevisions: 0, environmentRevisions: 0 },
  retiredAt: value === 'retired' ? '2026-01-01T00:00:00Z' : undefined,
}));
const hosts: EnvironmentHost[] = [
  { name: 'node-a', address: 'node-a.example.test', groups: ['group-a'], user: 'operator', port: 22 },
  { name: 'node-b', address: 'node-b.example.test', groups: ['group-b'], user: 'operator', port: 2222 },
];

describe('environment inventory editor', () => {
  it('keeps canceled memberships separate and applies a valid group move without changing connections', async () => {
    const onChange = vi.fn();
    render(<EnvironmentInventoryEditor hosts={hosts} options={options} editable onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: '主机组管理' }));
    await userEvent.click(screen.getByRole('button', { name: '移出节点 node-a' }));
    expect(screen.getByRole('alert')).toHaveTextContent('node-a 尚未分组');
    expect(screen.getByRole('button', { name: '应用更改' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(onChange).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole('button', { name: '主机组管理' }));
    expect(screen.getByRole('button', { name: '移出节点 node-a' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '移出节点 node-a' }));
    await userEvent.click(screen.getByRole('button', { name: /^group-b / }));
    await userEvent.click(screen.getByRole('tab', { name: /添加节点/ }));
    await userEvent.type(screen.getByRole('textbox', { name: '搜索节点' }), 'node-a.example.test');
    await userEvent.click(screen.getByRole('button', { name: '添加当前结果' }));
    await userEvent.click(screen.getByRole('button', { name: '应用更改' }));
    expect(onChange).toHaveBeenCalledWith([{ ...hosts[0], groups: ['group-b'] }, hosts[1]]);
    expect(hosts[0].groups).toEqual(['group-a']);
  });

  it('adds only search results and preserves memberships in other groups', async () => {
    const onChange = vi.fn();
    const inventory = [...hosts, { name: 'node-c', address: 'node-c.example.test', groups: ['group-b'] }];
    render(<EnvironmentInventoryEditor hosts={inventory} options={options} editable onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: '主机组管理' }));
    await userEvent.click(screen.getByRole('tab', { name: /添加节点/ }));
    await userEvent.type(screen.getByRole('textbox', { name: '搜索节点' }), 'NODE-B');
    await userEvent.click(screen.getByRole('button', { name: '添加当前结果' }));
    await userEvent.click(screen.getByRole('button', { name: '应用更改' }));
    expect(onChange).toHaveBeenCalledWith([hosts[0], { ...hosts[1], groups: ['group-b', 'group-a'] }, inventory[2]]);
  });

  it('validates new nodes and removes a node together with its memberships from the local inventory', async () => {
    function Editor() {
      const [items, setItems] = useState(hosts);
      return <EnvironmentInventoryEditor hosts={items} options={options} editable onChange={setItems} />;
    }
    render(<Editor />);
    await userEvent.click(screen.getByRole('button', { name: '添加节点' }));
    const dialog = within(screen.getByRole('dialog', { name: '添加节点' }));
    expect(dialog.getByRole('button', { name: '添加节点' })).toBeDisabled();
    expect(dialog.queryByRole('checkbox', { name: /retired/ })).not.toBeInTheDocument();
    await userEvent.type(dialog.getByRole('textbox', { name: '节点名称' }), 'node-a');
    await userEvent.type(dialog.getByRole('textbox', { name: '节点地址' }), 'node-c.example.test');
    await userEvent.click(dialog.getByRole('checkbox', { name: 'group-b' }));
    await userEvent.click(dialog.getByRole('button', { name: '添加节点' }));
    expect(dialog.getByRole('alert')).toHaveTextContent('节点名称已存在');
    await userEvent.clear(dialog.getByRole('textbox', { name: '节点名称' }));
    await userEvent.type(dialog.getByRole('textbox', { name: '节点名称' }), 'node-c');
    await userEvent.click(dialog.getByRole('button', { name: '添加节点' }));
    expect(screen.getByText('node-c.example.test')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '移除节点 node-c，保存后生效' }));
    expect(screen.queryByText('node-c.example.test')).not.toBeInTheDocument();
    expect(screen.getAllByRole('row')).toHaveLength(3);
  });

  it('keeps retired groups visible but prevents changing their members', async () => {
    render(<EnvironmentInventoryEditor hosts={[{ ...hosts[0], groups: ['retired'] }]} options={options} editable onChange={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: '主机组管理' }));
    await userEvent.click(screen.getByRole('button', { name: /retired 已退役/ }));
    expect(screen.queryByRole('tab', { name: /添加节点/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '移出节点 node-a' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '取消' }));
    await userEvent.click(screen.getByRole('button', { name: '编辑节点 node-a' }));
    expect(screen.getByRole('checkbox', { name: 'retired（已退役）' })).toBeDisabled();
  });

  it('shows nodes and group labels without mutation controls to read-only viewers', () => {
    render(<EnvironmentInventoryEditor hosts={hosts} options={options} editable={false} onChange={vi.fn()} />);
    expect(screen.getByText('node-a')).toBeInTheDocument();
    expect(screen.getByText('group-a')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
