import { useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

// Own only the URL contract; editor drafts and business actions belong to their flows.
export function useComponentSelection() {
  const [searchParams, setSearchParams] = useSearchParams();
  const handledDeepLink = useRef<string>();
  const action = searchParams.get('action');
  if (!action) handledDeepLink.current = undefined;
  function consumeAction(componentId: string, releaseId: string, handle: (action: string) => void) {
    if (!action) return;
    const key = `${componentId}:${releaseId}:${action}`;
    if (handledDeepLink.current === key) return;
    handledDeepLink.current = key;
    handle(action);
    const next = new URLSearchParams(searchParams); next.delete('action');
    setSearchParams(next, { replace: true });
  }
  function focusParameters() {
    if (searchParams.get('focus') !== 'parameters') return;
    const target = document.getElementById('contract-parameters');
    if (!target) return;
    target.scrollIntoView?.({ block: 'start' });
    const next = new URLSearchParams(searchParams); next.delete('focus');
    setSearchParams(next, { replace: true });
  }
  return {
    searchParams, setSearchParams, selectedId: searchParams.get('selected') ?? undefined,
    selectedReleaseId: searchParams.get('release') ?? undefined, consumeAction, focusParameters
  };
}
