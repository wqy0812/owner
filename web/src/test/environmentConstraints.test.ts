import { describe, expect, it } from 'vitest';
import { activeEnvironmentConstraintDimensions, childOptionsForSelection, environmentConstraintDimensions, environmentConstraintGroups, parseConstraintSelection, serializeConstraintSelection, toggleConstraintValue, toggleHierarchicalConstraintValue } from '../types/environmentConstraints';
import type { PlatformOptionCategory } from '../types/domain';

const usage = { componentReleases: 0, scenarioRevisions: 0, environmentRevisions: 0 };
const categories: PlatformOptionCategory[] = [
  { id: 'arch', key: 'architecture', label: '架构', kind: 'environment_dimension', environmentRequired: true, sortOrder: 1, createdBy: 'seed', createdAt: '', usage, options: [
    { id: 'amd64', categoryId: 'arch', value: 'amd64', label: 'x86/amd64', sortOrder: 1, createdBy: 'seed', createdAt: '', usage },
    { id: 'arm64', categoryId: 'arch', value: 'arm64', label: 'ARM/arm64', sortOrder: 2, createdBy: 'seed', createdAt: '', usage },
  ] },
  { id: 'os', key: 'operatingSystem', label: '操作系统', kind: 'environment_dimension', environmentRequired: true, sortOrder: 2, createdBy: 'seed', createdAt: '', usage, options: [
    { id: 'kylin', categoryId: 'os', value: 'Kylin', label: 'Kylin', sortOrder: 1, createdBy: 'seed', createdAt: '', usage },
  ] },
  { id: 'custom', key: 'dimension_custom', label: 'CPU 厂商', kind: 'environment_dimension', environmentRequired: false, sortOrder: 3, createdBy: 'admin', createdAt: '', usage, options: [
    { id: 'vendor', categoryId: 'custom', value: 'option_vendor', label: '鲲鹏', sortOrder: 1, createdBy: 'admin', createdAt: '', usage },
  ] },
];
const dimensions = environmentConstraintDimensions(categories);

describe('dynamic environment constraints', () => {
  it('uses category order and display labels', () => {
    expect(environmentConstraintGroups({ architecture: ['amd64', 'arm64'], operatingSystem: ['Kylin'] }, dimensions)).toEqual([
      { key: 'architecture', label: '架构', values: ['x86/amd64', 'ARM/arm64'] },
      { key: 'operatingSystem', label: '操作系统', values: ['Kylin'] },
    ]);
  });

  it('automatically includes a newly added optional dimension', () => {
    const selection = parseConstraintSelection({ architecture: ['amd64'], dimension_custom: ['option_vendor'], unknown: ['x'] }, dimensions);
    expect(serializeConstraintSelection(toggleConstraintValue(selection, 'architecture', 'amd64'), dimensions)).toEqual({ dimension_custom: ['option_vendor'] });
  });

  it('ignores categories and values outside the current directory', () => {
    expect(parseConstraintSelection({ architecture: ['missing'], removed: ['value'] }, dimensions)).toEqual({ architecture: [], operatingSystem: [], dimension_custom: [] });
    expect(environmentConstraintGroups(undefined, dimensions)).toEqual([]);
  });

  it('filters child runtime versions by their explicit parent and hides retired dimensions', () => {
    const runtimeCategories: PlatformOptionCategory[] = [
      { id: 'runtime', key: 'containerRuntime', label: '容器运行时', kind: 'environment_dimension', environmentRequired: true, sortOrder: 1, createdBy: 'seed', createdAt: '', usage, options: [
        { id: 'docker', categoryId: 'runtime', value: 'docker', label: 'Docker', sortOrder: 1, createdBy: 'seed', createdAt: '', usage },
        { id: 'containerd', categoryId: 'runtime', value: 'containerd', label: 'containerd', sortOrder: 2, createdBy: 'seed', createdAt: '', usage },
      ] },
      { id: 'runtime-version', parentCategoryId: 'runtime', key: 'containerRuntimeVersion', label: '运行时版本', kind: 'environment_dimension', environmentRequired: true, sortOrder: 2, createdBy: 'seed', createdAt: '', usage, options: [
        { id: 'docker-version', categoryId: 'runtime-version', parentOptionId: 'docker', value: 'docker@20.10.21', label: '20.10.21', sortOrder: 1, createdBy: 'seed', createdAt: '', usage },
        { id: 'containerd-version', categoryId: 'runtime-version', parentOptionId: 'containerd', value: 'containerd@2.0.10', label: '2.0.10', sortOrder: 2, createdBy: 'seed', createdAt: '', usage },
      ] },
      { id: 'legacy', key: 'dockerVersion', label: 'Docker 版本', kind: 'environment_dimension', environmentRequired: false, retiredAt: '2026-09-02T00:00:00Z', sortOrder: 3, createdBy: 'seed', createdAt: '', usage, options: [] },
    ];
    const active = activeEnvironmentConstraintDimensions(runtimeCategories);
    expect(active.map((item) => item.key)).toEqual(['containerRuntime', 'containerRuntimeVersion']);
    expect(childOptionsForSelection(active[1], active[0], ['docker']).map((item) => item.value)).toEqual(['docker@20.10.21']);
    expect(childOptionsForSelection(active[1], active[0], ['docker', 'containerd']).map((item) => item.value)).toEqual(['docker@20.10.21', 'containerd@2.0.10']);
    expect(childOptionsForSelection(active[0], undefined, []).map((item) => item.value)).toEqual(['docker', 'containerd']);
    expect(childOptionsForSelection({ ...active[0], options: [...active[0].options, { ...active[0].options[0], id: 'retired-runtime', value: 'retired', retiredAt: '2026-09-02T00:00:00Z' }] }, undefined, []).map((item) => item.value)).toEqual(['docker', 'containerd']);
    expect(childOptionsForSelection({ ...active[1], options: [...active[1].options, { ...active[1].options[0], id: 'orphan', value: 'orphan@1', parentOptionId: undefined }] }, active[0], ['docker']).map((item) => item.value)).toEqual(['docker@20.10.21']);
    const selection = { containerRuntime: ['docker', 'containerd'], containerRuntimeVersion: ['docker@20.10.21', 'containerd@2.0.10'] };
    expect(toggleHierarchicalConstraintValue(selection, active, 'containerRuntime', 'docker')).toEqual({ containerRuntime: ['containerd'], containerRuntimeVersion: ['containerd@2.0.10'] });
    expect(toggleHierarchicalConstraintValue({ containerRuntime: ['docker'] }, active, 'containerRuntime', 'containerd')).toEqual({ containerRuntime: ['docker', 'containerd'], containerRuntimeVersion: [] });
    expect(toggleHierarchicalConstraintValue(selection, active, 'containerRuntimeVersion', 'docker@20.10.21').containerRuntimeVersion).toEqual(['containerd@2.0.10']);
    expect(toggleHierarchicalConstraintValue(selection, active, 'missing', 'value').missing).toEqual(['value']);
  });

  it('normalizes scalar, nested array, empty, and object constraint values', () => {
    expect(environmentConstraintGroups({ architecture: 'amd64' }, dimensions)[0].values).toEqual(['x86/amd64']);
    expect(environmentConstraintGroups({ architecture: 'unknown-architecture' }, dimensions)[0].values).toEqual(['unknown-architecture']);
    expect(environmentConstraintGroups({ architecture: [['amd64'], '', null], operatingSystem: {} }, dimensions)).toEqual([{ key: 'architecture', label: '架构', values: ['x86/amd64'] }]);
    expect(serializeConstraintSelection({}, dimensions)).toEqual({});
  });
});
