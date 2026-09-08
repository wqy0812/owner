/** DOM order breaks ties for nested dialogs; the update guard has a higher z-index. */
export function topModal(): HTMLElement | undefined {
  return [...document.querySelectorAll<HTMLElement>('.ant-modal')]
    .filter(element => element.getClientRects().length)
    .sort((left, right) => {
      const layer = (element: HTMLElement) => Number(getComputedStyle(element.closest('.ant-modal-wrap') ?? element).zIndex) || 0;
      return layer(left) - layer(right);
    }).at(-1);
}

export function restoreModalFocus(trigger: HTMLElement | null) {
  if (!trigger?.isConnected || trigger.matches(':disabled') || trigger.closest('[inert]')) return;
  const top = topModal();
  if (!top || top.contains(trigger)) trigger.focus({ preventScroll: true });
}
