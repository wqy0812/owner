import { topModal, restoreModalFocus } from './modalFocus';
import { App as AntApp, ConfigProvider, type ThemeConfig } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react';
import { Input } from 'antd';

export const desktopTheme: ThemeConfig = {
  token: {
    colorPrimary: '#1677ff', colorBgLayout: '#f5f7fa', colorBgContainer: '#ffffff',
    colorText: '#1f2329', colorTextSecondary: '#646a73', colorBorder: '#d9dfe8',
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", Arial, sans-serif',
    fontFamilyCode: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
    fontSize: 14, fontSizeSM: 12, lineHeight: 22 / 14, controlHeight: 32,
    borderRadius: 6, borderRadiusLG: 8,
  },
  components: {
    Form: { itemMarginBottom: 16, verticalLabelPadding: '0 0 8px' },
    Modal: { titleFontSize: 18, titleLineHeight: 26 / 18 },
    Table: { cellPaddingBlock: 10, cellPaddingInline: 16 },
    Tabs: { horizontalMargin: '0 0 16px 0' },
  },
};

interface Dialogs {
  confirm: (content: ReactNode, options?: { title?: string; okText?: string; danger?: boolean }) => Promise<boolean>;
  prompt: (content: string, initialValue?: string) => Promise<string | null>;
}
const DialogContext = createContext<Dialogs | null>(null);

function DialogProvider({ children }: { children: ReactNode }) {
  const { modal } = AntApp.useApp();
  const lastControl = useRef<HTMLElement | null>(null);
  // Supplement Ant's focus trap at browser chrome and mask boundaries, including
  // context confirmations. Leave popup keyboard handling to its own component.
  useEffect(() => {
    const rememberControl = (event: FocusEvent) => {
      if (event.target instanceof HTMLElement && event.target.tabIndex >= 0 && !event.target.matches(':disabled')) lastControl.current = event.target;
    };
    let compositionEndedAt = 0;
    const compositionEnd = () => { compositionEndedAt = Date.now(); };
    function preserveMaskFocus(event: MouseEvent) {
      if (!(event.target instanceof HTMLElement) || !event.target.matches('.ant-modal-wrap, .ant-modal-mask')) return;
      const dialog = topModal();
      if (!dialog) return;
      event.preventDefault();
      if (!dialog.contains(document.activeElement)) dialog.focus({ preventScroll: true });
    }
    function trapTab(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.isComposing && Date.now() - compositionEndedAt > 200) {
        const dialog = topModal();
        const popup = [...document.querySelectorAll<HTMLElement>('.ant-select-dropdown, .ant-dropdown, .ant-picker-dropdown')].some(element => element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden');
        // Ant's portal stack can retain a closed Select above the dialog. Use the
        // actual top dialog's existing cancel action once its popup is hidden.
        if (dialog && !popup) {
          event.preventDefault(); event.stopPropagation();
          dialog.querySelector<HTMLButtonElement>('.ant-modal-close:not(:disabled), .ant-modal-confirm-btns .ant-btn-default:not(:disabled)')?.click();
          return;
        }
      }
      if (event.key !== 'Tab' || event.defaultPrevented) return;
      const dialog = topModal();
      if (!dialog) return;
      const focusable = [...dialog.querySelectorAll<HTMLElement>('button, input, select, textarea, a[href], [tabindex]')]
        .filter(element => element.tabIndex >= 0 && !element.matches(':disabled') && element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden');
      if (!focusable.length || document.activeElement === (event.shiftKey ? focusable[0] : focusable.at(-1))) {
        event.preventDefault();
        ((event.shiftKey ? focusable.at(-1) : focusable[0]) ?? dialog).focus({ preventScroll: true });
      }
    }
    document.addEventListener('focusin', rememberControl);
    document.addEventListener('mousedown', preserveMaskFocus, true);
    document.addEventListener('keydown', trapTab, true);
    document.addEventListener('compositionend', compositionEnd);
    return () => { document.removeEventListener('focusin', rememberControl); document.removeEventListener('mousedown', preserveMaskFocus, true); document.removeEventListener('keydown', trapTab, true); document.removeEventListener('compositionend', compositionEnd); };
  }, []);

  const pending = useRef(new Map<() => void, () => void>());
  useEffect(() => () => { for (const [cancel, destroy] of pending.current) { cancel(); destroy(); } pending.current.clear(); }, []);
  const confirm: Dialogs['confirm'] = (content, options = {}) => new Promise(resolve => {
    if (pending.current.size) { resolve(false); return; }
    const trigger = lastControl.current;
    let settled = false;
    const finish = (accepted: boolean) => { if (!settled) { settled = true; pending.current.delete(cancel); resolve(accepted); } };
    const cancel = () => finish(false);
    const instance = modal.confirm({
      title: options.title ?? '确认操作', content: <div className="dialog-confirm-content">{content}</div>,
      width: 480, centered: true, mask: { closable: false, blur: false },
      focusable: { autoFocusButton: 'cancel', trap: true, focusTriggerAfterClose: true },
      okText: options.okText ?? '确认', cancelText: '取消',
      okButtonProps: { danger: options.danger ?? (typeof content === 'string' && /删除|废弃|放弃/.test(content)) },
      onOk: () => finish(true), onCancel: cancel, afterClose: () => { cancel(); requestAnimationFrame(() => restoreModalFocus(trigger)); },
    });
    pending.current.set(cancel, instance.destroy);
  });
  const prompt: Dialogs['prompt'] = (content, initialValue = '') => new Promise(resolve => {
    if (pending.current.size) { resolve(null); return; }
    let value = initialValue;
    const trigger = lastControl.current;
    let settled = false;
    const finish = (result: string | null) => { if (!settled) { settled = true; pending.current.delete(cancel); resolve(result); } };
    const cancel = () => finish(null);
    const instance = modal.confirm({
      title: '编辑内容', width: 720, centered: true, mask: { closable: false, blur: false },
      content: <div className="dialog-input-content"><p>{content}</p><Input aria-label={content} defaultValue={initialValue} onChange={event => { value = event.target.value; }} /></div>,
      focusable: { autoFocusButton: 'cancel', trap: true, focusTriggerAfterClose: true },
      okText: '保存', cancelText: '取消', onOk: () => finish(value), onCancel: cancel, afterClose: () => { cancel(); requestAnimationFrame(() => restoreModalFocus(trigger)); },
    });
    pending.current.set(cancel, instance.destroy);
  });
  return <DialogContext.Provider value={{ confirm, prompt }}>{children}</DialogContext.Provider>;
}

export function UIProvider({ children }: { children: ReactNode }) {
  return <ConfigProvider locale={zhCN} theme={desktopTheme} button={{ autoInsertSpace: false }}><AntApp notification={{ placement: 'bottomRight', pauseOnHover: false }}><DialogProvider>{children}</DialogProvider></AntApp></ConfigProvider>;
}

export function useDialogs() {
  const context = useContext(DialogContext);
  if (!context) throw new Error('useDialogs must be used within UIProvider');
  return context;
}
