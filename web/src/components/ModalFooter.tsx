import { createContext, useContext, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

export const ModalFooterContext = createContext<HTMLElement | null>(null);
/** Place an embedded workflow's primary actions in the containing modal's fixed footer. */
export function ModalFooter({ children }: { children: ReactNode }) {
  const target = useContext(ModalFooterContext);
  const actions = <div className="modal-actions">{children}</div>;
  return target ? createPortal(actions, target) : actions;
}
