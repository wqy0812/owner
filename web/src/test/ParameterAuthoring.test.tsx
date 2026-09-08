import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import { ParameterTable, parameterContractErrors } from '../components/ParameterEditors';
import type { ParameterDefinition } from '../types/domain';
import { selectOption } from './antdInteractions';
import { fireEvent, render, screen } from './render';
import { user } from './interactions';

function Editor({ initial, saved }: { initial: ParameterDefinition; saved: (parameters: ParameterDefinition[]) => void }) {
  const [parameters, setParameters] = useState([initial]);
  const [valid, setValid] = useState(true);
  return <><ParameterTable parameters={parameters} onChange={setParameters} onValidationChange={setValid}/><button disabled={!valid || parameterContractErrors(parameters, [], []).length > 0} onClick={() => saved(parameters)}>提交合同</button></>;
}
const base: ParameterDefinition = { name: 'value', description: 'Typed input', type: 'string', visibility: 'public', valueProvider: 'component_owner', modifiable: false, fixedValue: 'fixed' };

it('keeps focus while typing a parameter name and clears stale data on type/provider changes', async () => {
  const saved = vi.fn(); render(<Editor initial={base} saved={saved}/>);
  await user.click(screen.getByRole('button', { name: '编辑参数 value' }));
  const name = screen.getByRole('textbox', { name: '参数名称' });
  await user.clear(name); await user.type(name, 'worker_count');
  expect(name).toHaveFocus(); expect(name).toHaveValue('worker_count');
  await selectOption(screen.getByRole('combobox', { name: '参数类型' }), 'integer');
  expect(screen.getByRole('button', { name: '提交合同' })).toBeDisabled();
  await selectOption(screen.getByRole('combobox', { name: '值的负责人' }), '环境 Owner 填写');
  expect(screen.getByRole('checkbox', { name: '允许外部修改' })).toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: '提交合同' }));
  expect(saved).toHaveBeenCalledWith([expect.objectContaining({ name: 'worker_count', type: 'integer', environmentBinding: { kind: 'private' }, fixedValue: undefined, suggestedValue: undefined, testValue: undefined })]);
});

it.each([
  ['object', [{ mode: 'safe' }, { mode: 'fast' }]],
  ['array', [['a'], ['b', 'c']]],
  ['integer', [1, 2]],
  ['boolean', [false, true]],
] as const)('preserves %s enum value types through editing and submission', async (type, values) => {
  const saved = vi.fn();
  render(<Editor initial={{ ...base, type, valueProvider: 'scenario_owner', modifiable: true, fixedValue: undefined }} saved={saved}/>);
  fireEvent.click(screen.getByRole('button', { name: '编辑参数 value' }));
  const input = screen.getByRole('textbox', { name: '枚举' });
  fireEvent.change(input, { target: { value: JSON.stringify(values) } });
  expect(screen.getByRole('button', { name: '提交合同' })).toBeEnabled();
  fireEvent.click(screen.getByRole('button', { name: '提交合同' }));
  expect(saved).toHaveBeenLastCalledWith([expect.objectContaining({ enum: values })]);
  fireEvent.change(input, { target: { value: '[' } });
  expect(screen.getByRole('button', { name: '提交合同' })).toBeDisabled();
  expect(screen.getByRole('alert')).toHaveTextContent('JSON 数组');
  fireEvent.click(screen.getByRole('button', { name: '收起参数 value' }));
  fireEvent.click(screen.getByRole('button', { name: '编辑参数 value' }));
  expect(screen.getByRole('textbox', { name: '枚举' })).toHaveValue('[');
  fireEvent.change(screen.getByRole('textbox', { name: '枚举' }), { target: { value: JSON.stringify(values) } });
  expect(screen.getByRole('button', { name: '提交合同' })).toBeEnabled();
});
