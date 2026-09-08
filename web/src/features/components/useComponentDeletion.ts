import { useDialogs } from '../../components/UIProvider';
import { useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import { displayError, useApp } from '../../context/AppContext';
import type { Component } from '../../types/domain';
export function useComponentDeletion({ scopeKey, onDeleted, onRejected }: {
    scopeKey: string;
    onDeleted: (id: string) => void;
    onRejected: () => void;
}) {
    const { confirm } = useDialogs();
    const { user, notify, signalRefresh } = useApp();
    const [pendingScope, setPendingScope] = useState<string>();
    const pending = useRef(new Set<string>());
    const generation = useRef(0);
    useEffect(() => {
        generation.current += 1;
        return () => { generation.current += 1; };
    }, [scopeKey]);
    async function deleteComponent(component: Component) {
        if (!component.canDelete || component.ownerId !== user.id || user.role !== 'component_owner' || pending.current.has(scopeKey))
            return;
        const current = generation.current;
        if (!await confirm(`确认永久删除空组件“${component.name}”？\n仅没有任何版本且没有引用的组件可以删除；历史审计记录将保留。\n\n此操作不可恢复。`))
            return;
        if (current !== generation.current || pending.current.has(scopeKey)) return;
        pending.current.add(scopeKey);
        setPendingScope(scopeKey);
        try {
            await api.deleteComponent(component.id);
            if (current === generation.current) {
                onDeleted(component.id);
                notify('success', '组件已永久删除', component.name);
            }
            signalRefresh(['components', 'workbench']);
        }
        catch (reason) {
            if (current !== generation.current)
                return;
            onRejected();
            notify('error', '删除组件失败', displayError(reason));
            signalRefresh('components');
        }
        finally {
            pending.current.delete(scopeKey);
            setPendingScope(active => active === scopeKey ? undefined : active);
        }
    }
    return { deleting: pendingScope === scopeKey, deleteComponent };
}
