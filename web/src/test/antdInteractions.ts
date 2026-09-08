import { fireEvent, isInaccessible, screen, waitFor, within } from '@testing-library/react';
import { user } from './interactions';

export async function optionsFor(control: HTMLElement) {
  if (control.getAttribute('aria-expanded') !== 'true') fireEvent.mouseDown(control.closest('.ant-select')!.querySelector('.ant-select-content') ?? control);
  return waitFor(() => {
    const id = control.getAttribute('aria-controls');
    if (!id) throw new Error('The expanded Select must identify its listbox with aria-controls');
    const list = document.getElementById(id);
    if (!list || list.getAttribute('role') !== 'listbox') throw new Error(`Select listbox ${id} is not ready`);
    return list;
  });
}
export async function selectOption(control: HTMLElement, label: string | RegExp) {
  const list = await optionsFor(control);
  await user.click(within(list).getByRole('option', { name: label }));
}
export async function answerConfirm(accepted = true) {
  const dialog = await screen.findByRole('dialog', { name: '确认操作' });
  await user.click(within(dialog).getByRole('button', { name: accepted ? '确认' : '取消' }));
  await waitFor(() => { if (dialog.isConnected) throw new Error('Confirmation has not closed'); });
}
export async function moreAction(label: string | RegExp, trigger = '更多操作') {
  const button = await waitFor(() => {
    const visible = screen.getAllByLabelText(trigger).filter(element => !isInaccessible(element));
    if (visible.length !== 1) throw new Error(`Expected one visible ${trigger} trigger, found ${visible.length}`);
    return visible[0];
  });
  await user.click(button);
  const menu = await screen.findByRole('menu');
  await user.click(within(menu).getByRole('menuitem', { name: label }));
}
