import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { DependencyContractList, DependencyEditor, ParameterContractList, ParameterTable, defaultContractRelease, describeParameterMapping, parameterContractErrors } from '../components/ParameterEditors';
import { isNodeReachable } from '../pages/ScenariosPage';
import type { Component, ComponentRelease, ParameterDefinition } from '../types/domain';

const readiness: ComponentRelease['readiness'] = { status: 'ready', blockers: [] };

const kubeletRelease: ComponentRelease = {
  id: 'release-kubelet',
  componentId: 'component-kubelet',
  version: '1.17.5',
  state: 'released',
  readiness,
  parameters: [
    { name: 'kubeInstallRoot', description: 'kubelet 安装根目录', type: 'string', visibility: 'public' },
    { name: 'K8S_VERSION', description: 'Kubernetes 版本', type: 'string', visibility: 'internal' },
  ],
};

const kubelet: Component = {
  id: 'component-kubelet',
  name: 'kubelet',
  ownerId: 'alice',
  layer: 'orchestration_core',
  tags: ['worker', 'core'],
  releases: [kubeletRelease],
};

const proxyRelease: ComponentRelease = {
  id: 'release-kube-proxy',
  componentId: 'component-kube-proxy',
  version: '1.17.5',
  state: 'released',
  readiness,
  parameters: [{ name: 'kubeRoot', description: '复用 kubelet 安装目录', type: 'string', visibility: 'internal' }],
  dependencies: [{
    componentId: 'component-kubelet',
    componentName: 'kubelet',
    releaseId: 'release-kubelet',
    version: '1.17.5',
    purpose: '复用 kubelet 安装目录',
    parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }],
  }],
};

const proxy: Component = {
  id: 'component-kube-proxy',
  name: 'kube-proxy',
  ownerId: 'alice',
  layer: 'orchestration_core',
  tags: ['network'],
  latestRelease: proxyRelease,
  releases: [proxyRelease],
};

describe('parameter contract editor', () => {
  it('only treats graph-reachable upstream nodes as dependency sources', () => {
    const edges = [{ source: 'upstream', target: 'middle' }, { source: 'middle', target: 'downstream' }, { source: 'unrelated', target: 'other' }];
    expect(isNodeReachable('upstream', 'downstream', edges)).toBe(true);
    expect(isNodeReachable('unrelated', 'downstream', edges)).toBe(false);
    expect(isNodeReachable('downstream', 'upstream', edges)).toBe(false);
  });

  it('filters public parameters and reports type mismatches', () => {
    const parameters: ParameterDefinition[] = [
      { name: 'kubeRoot', description: 'imported root', type: 'string', visibility: 'internal' },
      { name: 'count', description: 'replicas', type: 'integer', visibility: 'internal' },
    ];
    expect(parameterContractErrors(parameters, [{
      componentId: 'component-kubelet', releaseId: 'release-kubelet',
      parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'count' }],
    }], [kubeletRelease]).join(' ')).toContain('类型不一致');
    expect(parameterContractErrors(parameters, [{
      componentId: 'component-kubelet', releaseId: 'release-kubelet',
      parameterMappings: [{ upstreamParameter: 'kubeInstallRoot', targetParameter: 'kubeRoot' }],
    }], [kubeletRelease])).toEqual([]);
    expect(parameterContractErrors(parameters, [{
      componentId: '', releaseId: '',
      parameterMappings: [],
    }], [kubeletRelease])).toContain('每项依赖必须锁定一个可用的上游版本');
    expect(parameterContractErrors(parameters, [{
      componentId: 'component-kubelet', releaseId: 'draft-kubelet',
      parameterMappings: [],
    }], [{ ...kubeletRelease, id: 'draft-kubelet', state: 'draft' }])).toEqual([]);
    expect(parameterContractErrors(parameters, [{
      componentId: 'component-kubelet', releaseId: 'release-kubelet', parameterMappings: [],
    }, {
      componentId: 'component-kubelet', releaseId: 'release-kubelet-alt', parameterMappings: [],
    }], [kubeletRelease, { ...kubeletRelease, id: 'release-kubelet-alt' }])).toContain('组件 component-kubelet 只能添加一项直接依赖');
  });

  it('lets the owner add a parameter and mark it public', async () => {
    const seen: ParameterDefinition[][] = [];
    render(<ParameterTable parameters={[{ name: 'kubeInstallRoot', description: 'root', type: 'string', required: true, visibility: 'internal' }]} onChange={(parameters) => seen.push(parameters)} />);
    expect(screen.getByRole('radio', { name: /内部/ })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: /公开/ }));
    expect(seen.at(-1)?.[0]?.visibility).toBe('public');
    await userEvent.click(screen.getByRole('button', { name: '新增参数' }));
    expect(seen.at(-1)?.at(-1)).toEqual({ name: '', description: '', type: 'string', required: false, visibility: 'internal' });
  });

  it('lets the owner pick a component before locking one of its released versions', async () => {
    const kubeletV2: ComponentRelease = { ...kubeletRelease, id: 'release-kubelet-2', version: '1.18.0' };
    const kubeletWithTwo: Component = { ...kubelet, releases: [kubeletRelease, kubeletV2] };
    const containerd: Component = {
      id: 'component-containerd',
      name: 'containerd',
      ownerId: 'alice',
      layer: 'runtime_state',
      tags: ['runtime'],
      releases: [{ id: 'release-containerd', componentId: 'component-containerd', version: 'v2.1.1', state: 'released', readiness, parameters: [] }],
    };
    const seen: ComponentRelease['dependencies'][] = [];
    const view = (dependencies: NonNullable<ComponentRelease['dependencies']>) => (
      <DependencyEditor
        dependencies={dependencies}
        components={[kubeletWithTwo, containerd]}
        currentParameters={[{ name: 'kubeRoot', description: 'imported', type: 'string', visibility: 'internal' }]}
        currentComponentId="component-kube-proxy"
        onChange={(next) => seen.push(next)}
      />
    );
    const { rerender } = render(view([{ componentId: '', releaseId: '', purpose: '', parameterMappings: [] }]));
    expect(screen.getByLabelText('已发布版本')).toBeDisabled();
    expect(screen.getByLabelText('上游组件')).toContainHTML('kubelet');
    expect(screen.getByLabelText('上游组件')).toContainHTML('containerd');
    expect(screen.getByLabelText('已发布版本')).not.toContainHTML('1.17.5');
    await userEvent.selectOptions(screen.getByLabelText('上游组件'), 'component-kubelet');
    expect(seen.at(-1)?.[0]).toMatchObject({ componentId: 'component-kubelet', releaseId: '' });
    rerender(view([{ componentId: 'component-kubelet', releaseId: '', purpose: '', parameterMappings: [] }]));
    const versionSelect = screen.getByLabelText('已发布版本');
    expect(versionSelect).toBeEnabled();
    expect(versionSelect).toContainHTML('1.17.5');
    expect(versionSelect).toContainHTML('1.18.0');
    expect(versionSelect).not.toContainHTML('v2.1.1');
    await userEvent.selectOptions(versionSelect, 'release-kubelet-2');
    expect(seen.at(-1)?.[0]).toMatchObject({ componentId: 'component-kubelet', releaseId: 'release-kubelet-2' });
  });

  it('only offers upstream public parameters when mapping a dependency', async () => {
    const seen: ComponentRelease['dependencies'][] = [];
    render(<DependencyEditor
      dependencies={[{ componentId: 'component-kubelet', releaseId: 'release-kubelet', purpose: '', parameterMappings: [{ upstreamParameter: '', targetParameter: '' }] }]}
      components={[kubelet]}
      currentParameters={[{ name: 'kubeRoot', description: 'imported', type: 'string', visibility: 'internal' }]}
      currentComponentId="component-kube-proxy"
      onChange={(dependencies) => seen.push(dependencies)}
    />);
    const source = screen.getByLabelText('上游公开参数');
    expect(source).toContainHTML('kubeInstallRoot');
    expect(source).not.toContainHTML('K8S_VERSION');
    await userEvent.selectOptions(source, 'kubeInstallRoot');
    expect(seen.at(-1)?.[0]?.parameterMappings).toEqual([{ upstreamParameter: 'kubeInstallRoot', targetParameter: '' }]);
  });

  it('defaults the contract view to a release that has mappings', () => {
    const empty: ComponentRelease = { id: 'new', componentId: 'component-kube-proxy', version: '1.34.3', state: 'released', readiness };
    expect(defaultContractRelease([empty, proxyRelease], empty)?.id).toBe('release-kube-proxy');
  });

  it('describes which upstream component parameter a local parameter comes from', () => {
    expect(describeParameterMapping(proxyRelease.dependencies![0], proxyRelease.dependencies![0].parameterMappings![0], [kubelet, proxy]))
      .toBe('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot');
  });

  it('renders the contract so a reader can see public, private and imported parameters', () => {
    render(<>
      <DependencyContractList dependencies={proxyRelease.dependencies ?? []} components={[kubelet, proxy]} />
      <ParameterContractList release={proxyRelease} components={[kubelet, proxy]} />
    </>);
    expect(screen.getAllByText('本组件参数 kubeRoot 来自 kubelet 1.17.5 的公开参数 kubeInstallRoot').length).toBeGreaterThan(1);
    expect(screen.getByText('内部')).toBeInTheDocument();
    expect(screen.getByText('kubeRoot')).toBeInTheDocument();
  });
});
