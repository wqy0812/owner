import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { LegacyYamlNotice } from '../components/LegacyYamlNotice';
import { ResourceContractEditor } from '../components/ResourceContractEditor';

it('keeps resource claims separate from component-owned YAML checks', () => {
  render(<ResourceContractEditor value={{version: 1, noManagedPaths: true, claims: []}} onChange={vi.fn()}/>);
  expect(screen.queryByText('运行条件与残留资源探测')).not.toBeInTheDocument();
  expect(screen.queryByText('添加只读探测')).not.toBeInTheDocument();
  expect(screen.getByText(/运行条件与残留探测请录入前置检查 YAML/)).toBeInTheDocument();
});

it('shows immutable legacy details and requires explicit migration confirmation', () => {
  const onConfirm=vi.fn();
  render(<LegacyYamlNotice value={{gatherFacts: true, checks: [{id:'python',kind:'command',target:'python3'}]}} onConfirm={onConfirm}/>);
  expect(screen.getByText('python3')).toBeInTheDocument();
  const checkbox=screen.getByRole('checkbox');
  expect(checkbox).not.toBeChecked();
  expect(onConfirm).not.toHaveBeenCalled();
  fireEvent.click(checkbox);
  expect(onConfirm).toHaveBeenCalledWith(true);
});
