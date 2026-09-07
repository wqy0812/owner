import type { ComponentRelease, ParameterDefinition, ScenarioAcceptanceJob, ScenarioNode, ScenarioNodeData } from '../../types/domain';

// getRandomValues is available on the platform's HTTP origins as well as HTTPS.
export function newScenarioClientID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function newAcceptanceJob(index: number, hostGroup = ''): ScenarioAcceptanceJob {
  return { id: `acceptance-${newScenarioClientID()}`, name: `业务验收 ${index + 1}`, purpose: '', hostGroup,
    timeoutSeconds: 300, riskLevel: 'low', requiredCredentials: [], become: false,
    playbook: '', playbookSha256: '', mayMutate: false };
}
export function moveAcceptanceJob(jobs: ScenarioAcceptanceJob[], index: number, offset: number) {
  const target = index + offset;
  if (index < 0 || index >= jobs.length || target < 0 || target >= jobs.length) return jobs;
  const next = [...jobs]; [next[index], next[target]] = [next[target], next[index]]; return next;
}
export function scenarioParameterError(parameter: ParameterDefinition, value: unknown): string | undefined {
  if (value === undefined || value === null || value === '') return parameter.required ? `${parameter.name} 为必填项` : undefined;
  const validType = parameter.type === 'string' ? typeof value === 'string'
    : parameter.type === 'boolean' ? typeof value === 'boolean'
      : parameter.type === 'integer' ? typeof value === 'number' && Number.isInteger(value)
        : parameter.type === 'number' ? typeof value === 'number' && Number.isFinite(value)
          : parameter.type === 'array' ? Array.isArray(value)
            : typeof value === 'object' && !Array.isArray(value);
  if (!validType) return `${parameter.name} 必须是 ${parameter.type}`;
  if (parameter.minLength && typeof value === 'string' && [...value].length < parameter.minLength) return `${parameter.name} 长度不能小于 ${parameter.minLength}`;
  if (parameter.enum?.length && !parameter.enum.some(item => JSON.stringify(item) === JSON.stringify(value))) return `${parameter.name} 不在允许选项中`;
  return undefined;
}

// Keep every previous value and explicit dependency binding for review: an
// incompatible value is displayed as invalid until the Owner corrects it.
export function replaceScenarioNodeRelease(node: Pick<ScenarioNode, 'id' | 'data'>, release: ComponentRelease): ScenarioNodeData {
  if (release.componentId !== node.data.componentId) throw new Error('只能更换同一组件的版本');
  return { ...node.data, releaseId: release.id, version: release.version, action: 'install',
    hostGroup: release.actions?.find(action => action.type === 'install')?.hostGroup ?? '',
    parameterValues: { ...node.data.parameterValues }, dependencySources: { ...node.data.dependencySources } };
}
export function changedReleaseParameterIssues(node: Pick<ScenarioNode, 'id' | 'data'>, release: ComponentRelease) {
  const allowed = new Map((release.parameters ?? []).filter(p => p.modifiable && p.valueProvider === 'scenario_owner').map(p => [p.name, p]));
  return Object.entries(node.data.parameterValues ?? {}).flatMap(([name, value]) => {
    const definition = allowed.get(name);
    if (!definition) return [`失效参数 ${name} 已不属于目标版本的场景填写合同`];
    const issue = scenarioParameterError(definition, value); return issue ? [issue] : [];
  });
}

export function changedReleaseDependencyIssues(source: ComponentRelease | undefined, target: ComponentRelease): string[] {
  const describe = (release: ComponentRelease | undefined) => (release?.dependencies ?? []).map(dependency => ({
    key: dependency.id ?? dependency.releaseId,
    text: `${dependency.componentName ?? dependency.componentId} · ${dependency.version ?? dependency.releaseId}${dependency.parameterMappings?.length ? `；映射 ${dependency.parameterMappings.map(mapping => `${mapping.upstreamParameter} → ${mapping.targetParameter}`).join('、')}` : ''}`,
    contract: JSON.stringify([dependency.releaseId, dependency.kind, dependency.parameterMappings ?? []]),
  }));
  const before = describe(source); const after = describe(target);
  return [
    ...before.filter(item => !after.some(next => next.key === item.key && next.contract === item.contract)).map(item => `原依赖需迁移：${item.text}`),
    ...after.filter(item => !before.some(previous => previous.key === item.key && previous.contract === item.contract)).map(item => `目标依赖：${item.text}`),
  ];
}
