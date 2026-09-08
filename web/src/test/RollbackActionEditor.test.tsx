import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import { PlaybookActionEditor } from '../features/components/contract/PlaybookActionEditor';
import type { ActionDefinition } from '../types/domain';
import { optionsFor, selectOption } from './antdInteractions';
import { fireEvent, render, screen } from './render';

vi.mock('../context/AppContext', () => ({
  useApp: () => ({ notify: vi.fn(), platformOptionCategories: [] }),
  displayError: String,
}));
vi.mock('../features/components/contract/PlaybookWorkspaceEditor', () => ({ PlaybookWorkspaceEditor: () => null }));

function Editor() {
  const [actions, setActions] = useState<ActionDefinition[]>([
    { id: 'install', type: 'install', name: '部署示例', playbook: '', preCheckActionId: 'ready', postCheckActionId: 'ready' },
    { id: 'rollback', type: 'rollback', name: '恢复示例', playbook: '' },
    { id: 'ready', type: 'check', name: '初始条件', playbook: '' },
  ]);
  return <PlaybookActionEditor releaseId="release" releases={[]} actions={actions} onChange={setActions} onPersisted={setActions} onDirtyChange={vi.fn()} />;
}

it('keeps deployment prechecks and gives rollback only a postcheck selector', async () => {
  render(<Editor />);
  expect(screen.queryByRole('region', { name: '资源管理范围' })).not.toBeInTheDocument();
  expect(screen.queryByText('资源管理范围')).not.toBeInTheDocument();
  expect(screen.getByLabelText('前置检查').closest('.ant-select')).toHaveTextContent('初始条件');
  fireEvent.click(screen.getByRole('button', { name: '恢复示例' }));
  expect(screen.queryByLabelText('前置检查')).not.toBeInTheDocument();
  expect(screen.getByLabelText('后置检查').closest('.ant-select')).toHaveTextContent('复用被回滚动作的前置检查');
  await optionsFor(screen.getByLabelText('后置检查'));
  expect(screen.getByRole('option', { name: '复用被回滚动作的前置检查' })).toBeInTheDocument();
  await selectOption(screen.getByLabelText('后置检查'), '初始条件');
  expect(screen.getByLabelText('后置检查').closest('.ant-select')).toHaveTextContent('初始条件');
  fireEvent.click(screen.getByRole('button', { name: '部署示例' }));
  expect(screen.getByLabelText('前置检查').closest('.ant-select')).toHaveTextContent('初始条件');
});
