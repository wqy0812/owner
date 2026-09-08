import { createContext, useContext, useEffect, useId } from 'react';

export const ModalBusyContext = createContext<((id: string, busy: boolean) => void) | null>(null);
/** Editors report in-flight writes to the enclosing modal so its close controls are locked too. */
export function useModalBusy(busy: boolean) {
  const register = useContext(ModalBusyContext);
  const id = useId();
  useEffect(() => {
    register?.(id, busy);
    return () => register?.(id, false);
  }, [busy, id, register]);
}
