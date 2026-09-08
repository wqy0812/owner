import userEvent, { type UserEvent } from '@testing-library/user-event';
import { beforeEach } from 'vitest';

// A fresh input device per test keeps keyboard, pointer and clipboard state local.
export let user: UserEvent;
beforeEach(() => { user = userEvent.setup(); });

/** Fill a whole field through the clipboard, avoiding irrelevant per-character rerenders. */
export async function fillField(control: HTMLElement, value: string) {
  await user.clear(control);
  await user.click(control);
  await user.paste(value);
}
