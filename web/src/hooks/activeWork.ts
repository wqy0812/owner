import type { Workbench } from '../types/domain';

export function activeRun(run: { status: string }) {
  return ['running', 'queued'].includes(run.status);
}

export function activeWorkbench(workbench: Pick<Workbench, 'items'>) {
  return workbench.items.some(item => item.status === 'in_progress');
}
