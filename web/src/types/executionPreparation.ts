import type { ComponentTestRequest, WorkExplanation } from './domain';

export interface PreparationRequest { kind: 'component_test' | 'scenario_execution'; subjectId: string; environmentId: string; component?: ComponentTestRequest; scenario?: { environmentId: string; executionMode: string; testOnly: boolean } }
export interface PreparationSession { id: string; status: string; input: PreparationRequest; output: { checks: Array<{ id: string; category: string; label: string; host?: string; status: string; message?: string; source?: string; elapsedMs: number; startedAt?: string }>; plan?: unknown; error?: string; explanation?: WorkExplanation } }
