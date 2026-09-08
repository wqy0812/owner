import { restoreModalFocus } from './modalFocus';
import { ModalFooterContext } from './ModalFooter';
import { ModalBusyContext } from './ModalBusyContext';
import { Button } from 'antd';
import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Alert, ConfigProvider, Empty, Form, Modal as AntModal, Tag, type FormProps } from 'antd';
import { AlertCircle, Inbox, Info, LoaderCircle, RefreshCcw } from 'lucide-react';
import { STATUS_LABELS } from '../types/domain';
export function StatusPill({ status, children }: {
    status: string;
    children?: ReactNode;
}) {
    const normalized = status?.toLowerCase().replaceAll('-', '_') ?? 'unknown';
    const color = /failed|error|rejected|blocked|destructive/.test(normalized) ? 'error' : /^(succeeded|test_passed|passed|ready|approved|released|active|completed)$/.test(normalized) ? 'success' : /waiting|pending|draft|queued|warning/.test(normalized) ? 'warning' : /running|testing|processing/.test(normalized) ? 'processing' : 'default';
    return <Tag className={`status-pill status-pill--${normalized}`} color={color}>{children ?? STATUS_LABELS[normalized] ?? status}</Tag>;
}
export function InfoNote({ title, children }: {
    title?: string;
    children: ReactNode;
}) {
    return <Alert role="note" className="info-note" type="info" showIcon icon={<Info size={16}/>} title={title} description={children}/>;
}
export function PageHeader({ title, description, actions, }: {
    eyebrow?: string;
    title: string;
    description?: string;
    actions?: ReactNode;
}) {
    return (<header className="page-header">
      <div>

        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-header__actions">{actions}</div>}
    </header>);
}
export function LoadingBlock({ label = '加载中' }: {
    label?: string;
}) {
    return (<div className="state-block" role="status" aria-live="polite">
      <LoaderCircle className="spin" size={24}/>
      <span>{label}</span>
    </div>);
}
export function ErrorBlock({ message, onRetry, title = '暂时无法读取数据' }: {
    message: string;
    onRetry?: () => void;
    title?: string;
}) {
    return (<div className="state-block state-block--error" role="alert">
      <AlertCircle size={24}/>
      <div>
        <strong>{title}</strong>
        <span>{message}</span>
      </div>
      {onRetry && (<Button className="button button--quiet" onClick={onRetry} htmlType={"button"} type="default">
          <RefreshCcw size={15}/> 重试
        </Button>)}
    </div>);
}
export function RefreshNotice({ loading, error, onRetry }: {
    loading: boolean;
    error?: string;
    onRetry?: () => void;
}) {
    if (loading) {
        return <div className="state-block state-block--refresh" role="status"><LoaderCircle className="spin" size={16}/><span>正在刷新数据…</span></div>;
    }
    if (!error)
        return null;
    return (<div className="state-block state-block--error state-block--refresh" role="alert">
      <AlertCircle size={16}/>
      <div><strong>当前显示的是上次成功的数据</strong><span>{error}</span></div>
      {onRetry && <Button className="button button--quiet" onClick={onRetry} htmlType={"button"} type="default"><RefreshCcw size={15}/> 重试</Button>}
    </div>);
}
export function EmptyState({ title, description, action }: {
    title: string;
    description?: string;
    action?: ReactNode;
}) {
    return (<Empty className="empty-state" image={<Inbox size={32}/>} styles={{ image: { height: 32 } }} description={<><strong>{title}</strong>{description && <p>{description}</p>}</>}>{action}</Empty>);
}
export function Modal({ title, description, children, onClose, size = 'default', footer = null, busy = false, readOnly = false, dismissible = true, zIndex, formProps }: {
    title: string;
    description?: string;
    children: ReactNode;
    onClose: () => void;
    size?: 'small' | 'default' | 'wide' | 'workspace';
    footer?: ReactNode;
    busy?: boolean;
    readOnly?: boolean;
    dismissible?: boolean;
    zIndex?: number;
    formProps?: FormProps;
}) {
    const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
    const mountGeneration = useRef(0);
    useEffect(() => {
        const generation = ++mountGeneration.current;
        return () => queueMicrotask(() => {
            if (generation !== mountGeneration.current || !opener.current?.isConnected) return;
            restoreModalFocus(opener.current);
        });
    }, []);
    const descriptionId = useId();
    const [footerTarget, setFooterTarget] = useState<HTMLFieldSetElement | null>(null);
    const submitting = useRef(false);
    const [formBusy, setFormBusy] = useState(false);
    const [busyChildren, setBusyChildren] = useState<ReadonlySet<string>>(() => new Set());
    const registerBusy = useCallback((id: string, active: boolean) => setBusyChildren(current => {
        if (current.has(id) === active) return current;
        const next = new Set(current);
        if (active) next.add(id); else next.delete(id);
        return next;
    }), []);
    const locked = busy || formBusy || busyChildren.size > 0;
    async function finish(values: unknown) {
        if (locked || submitting.current) return;
        submitting.current = true;
        setFormBusy(true);
        try { await formProps?.onFinish?.(values); }
        finally { submitting.current = false; setFormBusy(false); }
    }
    const content = <AntModal open centered zIndex={zIndex} width={{ small: 480, default: 720, wide: 1040, workspace: 1440 }[size]}
        className={`desktop-modal desktop-modal--${size}`} title={<h2>{title}</h2>}
        aria-label={title} aria-describedby={description ? descriptionId : undefined}
        footer={<ConfigProvider componentDisabled={locked}><fieldset className="modal-footer-slot" ref={setFooterTarget} disabled={locked}
            {...(locked ? { inert: '' } : {})} onClickCapture={event => {
                if (locked || submitting.current) { event.preventDefault(); event.stopPropagation(); }
            }}>{footer}</fieldset></ConfigProvider>}
        onCancel={() => { if (dismissible && !locked && !submitting.current) onClose(); }} keyboard={dismissible && !locked} closable={dismissible ? { disabled: locked } : false}
        mask={{ closable: dismissible && readOnly && !locked, blur: false }} focusable={{ trap: true, focusTriggerAfterClose: true }} scrollLock destroyOnHidden
        modalRender={node => formProps ? <Form layout="vertical" preserve={false} {...formProps} disabled={locked} onFinish={finish}>{node}</Form> : <div>{node}</div>}>
        <ConfigProvider componentDisabled={locked}>{description && <p className="modal-description" id={descriptionId}>{description}</p>}{children}</ConfigProvider>
    </AntModal>;
    return <ModalBusyContext.Provider value={registerBusy}><ModalFooterContext.Provider value={footerTarget}>{content}</ModalFooterContext.Provider></ModalBusyContext.Provider>;
}
export function Metric({ label, value, note, tone = 'indigo' }: {
    label: string;
    value: string | number;
    note?: string;
    tone?: string;
}) {
    return (<article className={`metric metric--${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {note && <small>{note}</small>}
    </article>);
}
export function formatTime(value?: string): string {
    if (!value)
        return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime()))
        return value;
    return new Intl.DateTimeFormat('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
    }).format(date);
}
export function formatFileSize(bytes: number): string {
    if (bytes < 1024)
        return `${bytes} B`;
    if (bytes < 1048576)
        return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1073741824)
        return `${(bytes / 1048576).toFixed(1)} MB`;
    return `${(bytes / 1073741824).toFixed(1)} GB`;
}
