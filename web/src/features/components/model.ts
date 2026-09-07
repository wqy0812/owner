import type { ActionDefinition, Component, ComponentLayer, ComponentRelease, ComponentSummary, ReleaseEvidenceSummary, RunSummary } from '../../types/domain';

export const ACTION_OPTIONS: Array<{ value: ActionDefinition['type']; label: string }> = [
  { value: 'check', label: '检查动作' },
  { value: 'install', label: '安装' }, { value: 'configure', label: '配置' },
  { value: 'upgrade', label: '升级' },
  { value: 'rollback', label: '回滚' }, { value: 'uninstall', label: '卸载' },
];

export type ContractSection = 'dependencies' | 'parameters';

export type ContractEditIntent = ContractSection | 'all';

export type CatalogFilter = 'all' | 'mine' | 'draft' | 'attention';

export const REQUIRED_LIFECYCLE_ACTIONS: Array<{ type: ActionDefinition['type']; label: string }> = [
  { type: 'install', label: '安装' },
  { type: 'rollback', label: '回退' },
];

export function lifecycleSummary(actions: ActionDefinition[] = []) {
  const required = REQUIRED_LIFECYCLE_ACTIONS;
  const checks = new Set(actions.filter(action => action.type === 'check').map(action => action.id));
  const completed = required.filter((requiredAction) => actions.some(action => action.type === requiredAction.type && (action.type === 'rollback' ? (!action.preCheckActionId || checks.has(action.preCheckActionId)) && (!action.postCheckActionId || checks.has(action.postCheckActionId)) : checks.has(action.preCheckActionId ?? '') && checks.has(action.postCheckActionId ?? ''))));
  return {
    completed: completed.length,
    total: required.length,
    labels: completed.map((action) => action.label).join('、') || '尚未配置',
  };
}

export function releaseIsReady(release: ComponentRelease) {
  return release.readiness.status !== 'blocked';
}

export function componentNeedsAttention(component: ComponentSummary) {
  return component.needsAttention;
}

export function releaseEvidence(evidence: ReleaseEvidenceSummary | undefined, mode: 'install' | 'rollback') {
  return mode === 'install' ? evidence?.currentInstall ?? evidence?.historicalInstall : evidence?.currentRollback ?? evidence?.historicalRollback;
}

export function releaseReadyForPublish(release: ComponentRelease) {
  return releaseIsReady(release);
}

export function componentEvidenceLabel(release: ComponentRelease, run: RunSummary) {
  if (run.id === release.readiness.transitionEvidenceRunId) return '当前升级闭环证据';
  if (run.id === release.readiness.installEvidenceRunId) return '当前安装证据';
  if (run.id === release.readiness.rollbackEvidenceRunId) return '当前回退证据';
  return '历史证据';
}

export function componentRunLabel(run: RunSummary) {
  switch (run.action) {
    case 'rollback': return '回退验证';
    case 'upgrade': return '升级闭环验证';
    case 'configure': return '配置验证';
    case 'check': return '独立检查';
    case 'install': return '安装验证';
    default: return '组件验证';
  }
}

export function scenarioRunLabel(run: RunSummary) {
  return run.kind === 'scenario_test' ? '完整测试' : '正式运行';
}

export interface ScenarioRunEvidenceGroup {
  id: string;
  name: string;
  runs: RunSummary[];
  testCount: number;
  runCount: number;
}

export function isActionEntry(path: string) {
  return ACTION_OPTIONS.some((option) => path === `${option.value}.yml`);
}

export function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
}

export function splitCSV(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

export function componentInput(form: FormData): Partial<Component> {
  return {
    name: String(form.get('name')),
    slug: String(form.get('slug')),
    description: String(form.get('description')),
    layer: String(form.get('layer')) as ComponentLayer,
    tags: String(form.get('tags') ?? '').split(',').map((tag) => tag.trim()).filter(Boolean),
  };
}

export function publicCount(release: ComponentRelease) {
  return (release.parameters ?? []).filter((item) => item.visibility === 'public').length;
}
