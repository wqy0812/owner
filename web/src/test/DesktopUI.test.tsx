import { deferredTask } from './testLifecycle';
import { Button, Checkbox, Input, Select } from 'antd';
import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import { Field } from '../components/Field';
import { FilePicker } from '../components/FilePicker';
import { ParameterValueEditor } from '../components/ParameterEditors';
import { Modal } from '../components/Primitives';
import { useModalBusy } from '../components/ModalBusyContext';
import { ModalFooter } from '../components/ModalFooter';
import { useDialogs } from '../components/UIProvider';
import { WorkspaceTabs } from '../components/WorkspaceTabs';
import { selectOption } from './antdInteractions';
import { user as userEvent } from './interactions';
import { act, fireEvent, render, screen, waitFor } from './render';

it('submits named Ant controls once, preserves their types, and blocks closing while saving', async () => {
  let finish!: () => void;
  const save = vi.fn(() => deferredTask<void>(resolve => { finish = resolve; }));
  const close = vi.fn();
  render(<Modal title="保存配置" onClose={close} formProps={{onFinish:save}} footer={<><Button onClick={close}>取消</Button><Button htmlType="submit">保存</Button></>}>
    <Field label="名称" name="name" initialValue="示例"><Input /></Field>
    <Field label="风险" name="risk" initialValue="low"><Select options={[{value:'low',label:'低'},{value:'high',label:'高'}]} /></Field>
    <Field name="enabled" valuePropName="checked" initialValue={false}><Checkbox>启用</Checkbox></Field>
  </Modal>);
  await selectOption(screen.getByLabelText('风险'), '高');
  const form = screen.getByRole('button',{name:'保存'}).closest('form')!;
  fireEvent.submit(form); fireEvent.submit(form);
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));
  expect(save).toHaveBeenCalledWith({name:'示例',risk:'high',enabled:false});
  expect(screen.getByRole('button',{name:'取消'})).toBeDisabled();
  fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape',keyCode:27});
  expect(close).not.toHaveBeenCalled();
  await act(async()=>finish());
  expect(screen.getByRole('button',{name:'保存'})).toBeEnabled();
});

it('locks explicit footer buttons and portal actions until the child write completes', async () => {
  const close = vi.fn(), submit = vi.fn();
  function Editor({ busy }: { busy: boolean }) {
    useModalBusy(busy);
    return <ModalFooter><Button disabled={false} onClick={submit}>子操作</Button></ModalFooter>;
  }
  function Dialog({ busy }: { busy: boolean }) {
    return <Modal title="编辑 Draft" onClose={close} footer={<Button disabled={false} onClick={close}>取消</Button>}><Editor busy={busy}/></Modal>;
  }
  const view = render(<Dialog busy/>);
  for (const name of ['关闭', '取消', '子操作']) {
    const button = screen.getByRole('button', { name });
    expect(button).toBeDisabled();
    if (name !== '关闭') await userEvent.click(button);
  }
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', keyCode: 27 });
  expect(close).not.toHaveBeenCalled();
  expect(submit).not.toHaveBeenCalled();
  view.rerender(<Dialog busy={false}/>);
  await userEvent.click(screen.getByRole('button', { name: '子操作' }));
  await userEvent.click(screen.getByRole('button', { name: '取消' }));
  expect(submit).toHaveBeenCalledTimes(1);
  expect(close).toHaveBeenCalledTimes(1);
});

it('keeps editor content when switching tabs and isolates a newly opened object', async () => {
  function Editor({id}:{id:string}) {return <Modal key={id} title={`编辑 ${id}`} onClose={()=>{}} formProps={{}}><Field name="name" label="名称" initialValue={id}><Input /></Field></Modal>}
  const view=render(<Editor id="a"/>);
  await userEvent.type(screen.getByLabelText('名称'),' unsaved');
  view.rerender(<Editor id="b"/>);
  expect(screen.getByLabelText('名称')).toHaveValue('b');
  view.unmount();
  render(<WorkspaceTabs items={[{key:'edit',label:'编辑',children:<Input aria-label="草稿"/>},{key:'history',label:'历史',children:<p>历史内容</p>}]}/>);
  await userEvent.type(screen.getByLabelText('草稿'),'保留编辑内容');
  await userEvent.click(screen.getByRole('tab',{name:'历史'}));
  await userEvent.click(screen.getByRole('tab',{name:'编辑'}));
  expect(screen.getByLabelText('草稿')).toHaveValue('保留编辑内容');
});

it('resolves input cancellation as null and confirms an edited value through the themed dialog', async()=>{
  function Harness(){const {prompt}=useDialogs();const [value,setValue]=useState<string|null>('未操作');return <><Button onClick={async()=>setValue(await prompt('新的路径','templates/a.j2'))}>改名</Button><output data-testid="result">{JSON.stringify(value)}</output></>}
  render(<Harness/>);
  await userEvent.click(screen.getByRole('button',{name:'改名'}));
  await userEvent.clear(screen.getByLabelText('新的路径'));await userEvent.type(screen.getByLabelText('新的路径'),'templates/b.j2');
  await userEvent.click(screen.getByRole('button',{name:'取消'}));
  expect(screen.getByTestId('result')).toHaveTextContent('null');
  await userEvent.click(screen.getByRole('button',{name:'改名'}));
  expect(screen.getByLabelText('新的路径')).toHaveValue('templates/a.j2');
  await userEvent.clear(screen.getByLabelText('新的路径'));await userEvent.type(screen.getByLabelText('新的路径'),'templates/c.j2');
  await userEvent.click(screen.getByRole('button',{name:'保存'}));
  expect(screen.getByTestId('result')).toHaveTextContent('"templates/c.j2"');
});

it('preserves false, zero, empty and array parameter values', async()=>{
  const change=vi.fn();
  const view=render(<ParameterValueEditor parameter={{name:'开关',type:'boolean'}} value={true} optional onChange={change}/>);
  await selectOption(screen.getByLabelText('开关 的值'),'false');expect(change).toHaveBeenLastCalledWith(false);
  view.rerender(<ParameterValueEditor parameter={{name:'数量',type:'integer'}} value={1} onChange={change}/>);
  fireEvent.change(screen.getByLabelText('数量 的值'),{target:{value:'0'}});expect(change).toHaveBeenLastCalledWith(0);
  fireEvent.change(screen.getByLabelText('数量 的值'),{target:{value:''}});expect(change).toHaveBeenLastCalledWith(undefined);
  view.rerender(<ParameterValueEditor parameter={{name:'列表',type:'array'}} value={[]} onChange={change}/>);
  fireEvent.change(screen.getByLabelText('列表 的 JSON 值'),{target:{value:'[false,0,"",null]'}});fireEvent.blur(screen.getByLabelText('列表 的 JSON 值'));
  expect(change).toHaveBeenLastCalledWith([false,0,'',null]);
});

it('selects one file without making an automatic upload request',async()=>{
 const change=vi.fn(), request=vi.spyOn(XMLHttpRequest.prototype,'send');
 const {container}=render(<FilePicker onChange={change}/>);
 const file=new File(['FROM scratch'],'Dockerfile',{type:'text/plain'});
 await userEvent.upload(container.querySelector('input[type=file]')!,file);
 await waitFor(()=>expect(change).toHaveBeenCalledTimes(1));
 expect(change).toHaveBeenCalledWith(file);expect(request).not.toHaveBeenCalled();expect(screen.getByText('Dockerfile')).toBeVisible();request.mockRestore();
});
