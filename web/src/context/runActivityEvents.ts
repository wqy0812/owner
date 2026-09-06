export type RunActivitySignal = { type: 'log' | 'state' | 'refresh'; logId?: number };

// Log traffic stays outside React context state. Only the selected Run's
// subscriber is notified; a different identity receives a new dispatcher.
export class RunActivityEvents {
  private listeners = new Map<string, Set<(event: RunActivitySignal) => void>>();
  subscribe(runId: string, listener: (event: RunActivitySignal) => void) {
    const listeners = this.listeners.get(runId) ?? new Set();
    listeners.add(listener); this.listeners.set(runId, listeners);
    return () => { listeners.delete(listener); if (!listeners.size) this.listeners.delete(runId); };
  }
  emit(runId: string, event: RunActivitySignal) { this.listeners.get(runId)?.forEach(listener => listener(event)); }
  refresh() { for (const runId of this.listeners.keys()) this.emit(runId, { type: 'refresh' }); }
}
