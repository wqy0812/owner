import { Boxes, ChevronDown, ChevronRight, Filter, Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { EmptyState } from '../../../components/Primitives';
import { useApp } from '../../../context/AppContext';
import {
  COMPONENT_LAYERS
} from '../../../types/componentClassification';
import type { Component, ComponentLayer, ComponentRelease } from '../../../types/domain';
import { componentNeedsAttention, type CatalogFilter } from '../model';

import type { ReactNode } from 'react';
import type { ComponentSummary } from '../../../types/domain';
export function ComponentCatalog({ components, selected, detailId, editRelease, onSelect, children }: {
  components?: ComponentSummary[]; selected?: Component; detailId?: string; editRelease?: ComponentRelease;
  onSelect: (id: string) => boolean;
  children: (state: { selectedInCatalog: boolean; clearFilters: () => void }) => ReactNode;
}) {
  const { user } = useApp();
  const [catalogSearch, setCatalogSearch] = useState('');
  const [catalogFilter, setCatalogFilter] = useState<CatalogFilter>('all');
  const [layerOpen, setLayerOpen] = useState<Partial<Record<ComponentLayer, boolean>>>({});
  const filteredComponents = useMemo(() => {
    const query = catalogSearch.trim().toLocaleLowerCase();
    return (components ?? []).filter((component) => {
      const matchesQuery = !query || `${component.name} ${component.slug ?? ''}`.toLocaleLowerCase().includes(query);
      if (!matchesQuery) return false;
      if (catalogFilter === 'mine') return component.ownerId === user.id;
      if (catalogFilter === 'draft') return component.ownerId === user.id && component.hasDraft;
      if (catalogFilter === 'attention') return component.ownerId === user.id && componentNeedsAttention(component);
      return true;
    });
  }, [catalogFilter, catalogSearch, components, user.id]);
  const layeredComponents = COMPONENT_LAYERS.map((layer) => ({
    layer,
    components: filteredComponents.filter((component) => component.layer === layer.value),
  }));
  const selectedInCatalog = Boolean(selected && filteredComponents.some((component) => component.id === selected.id));
  function isLayerOpen(layer: ComponentLayer) {
    if (layerOpen[layer] !== undefined) return layerOpen[layer];
    return (components?.find((component) => component.id === detailId)?.layer ?? selected?.layer) === layer;
  }
  function toggleLayer(layer: ComponentLayer) {
    setLayerOpen((current) => ({ ...current, [layer]: !isLayerOpen(layer) }));
  }
  function setAllLayers(open: boolean) {
    setLayerOpen(Object.fromEntries(COMPONENT_LAYERS.map((layer) => [layer.value, open])));
  }
  function selectComponent(id: string) {
    if (!onSelect(id)) return;
    const component = components?.find(item => item.id === id);
    if (component) setLayerOpen(current => ({ ...current, [component.layer]: true }));
  }
  return <div className="catalog-layout">
    <aside className="catalog-list panel">
      <div className="catalog-list__header">
        <strong>组件目录</strong>
        <div className="catalog-list__tools">
          <span>{filteredComponents.length}/{components?.length ?? 0}</span>
          <button type="button" className="icon-text" onClick={() => setAllLayers(true)}>全部展开</button>
          <button type="button" className="icon-text" onClick={() => setAllLayers(false)}>全部折叠</button>
        </div>
      </div>
      <div className="catalog-search">
        <Search size={15} aria-hidden="true" />
        <input aria-label="搜索组件" value={catalogSearch} onChange={(event) => setCatalogSearch(event.target.value)} placeholder="搜索名称或标识" />
      </div>
      <div className="catalog-filters" aria-label="组件目录筛选">
        <Filter size={14} aria-hidden="true" />
        {([
          ['all', '全部'],
          ['mine', '我负责的'],
          ['draft', '有 Draft'],
          ['attention', '待处理'],
        ] as Array<[CatalogFilter, string]>).map(([value, label]) => <button key={value} type="button" className={catalogFilter === value ? 'active' : ''} aria-pressed={catalogFilter === value} title={value === 'attention' ? '我负责的组件中，尚无版本或有 Draft 的组件' : undefined} onClick={() => setCatalogFilter(value)}>{label}</button>)}
      </div>
      {catalogFilter === 'attention' ? <p>待处理：我负责且尚无版本或有 Draft 的组件。文件与验证问题请查看组件详情。</p> : null}
      {components?.length && filteredComponents.length ? layeredComponents.map(({ layer, components: layerComponents }) => {
        if ((catalogSearch || catalogFilter !== 'all') && !layerComponents.length) return null;
        const open = isLayerOpen(layer.value);
        return <section className={`catalog-layer${open ? '' : ' catalog-layer--collapsed'}`} key={layer.value}>
          <button type="button" className="catalog-layer__toggle" aria-expanded={open} aria-controls={`catalog-layer-${layer.value}`} onClick={() => toggleLayer(layer.value)}>
            <span>{layer.code}</span>
            <div><strong>{layer.label}</strong><small>{layerComponents.length} 个组件</small></div>
            {open ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight size={14} aria-hidden="true" />}
          </button>
          {open ? <div id={`catalog-layer-${layer.value}`}>{layerComponents.length ? layerComponents.map((component) => (
            <button key={component.id} type="button" disabled={Boolean(editRelease) && selected?.id !== component.id} className={`catalog-item${detailId === component.id ? ' active' : ''}`} onClick={() => selectComponent(component.id)}>
              <span className="catalog-item__icon"><Boxes size={18} /></span>
              <span><strong>{component.name}</strong><small>{component.tags.length ? component.tags.join(' · ') : '暂无标签'}</small></span>
              {component.ownerId === user.id && <span className="mine-dot" title="我负责的组件" />}
            </button>
          )) : <div className="catalog-layer__empty">本层暂无组件</div>}</div> : null}
        </section>;
      }) : components?.length ? <EmptyState title="没有匹配的组件" description="请调整搜索词或筛选条件。" /> : <EmptyState title="暂无组件" description="组件 Owner 可以创建第一个组件。" />}
    </aside>
    {children({ selectedInCatalog, clearFilters: () => { setCatalogSearch(''); setCatalogFilter('all'); } })}
  </div>;
}
