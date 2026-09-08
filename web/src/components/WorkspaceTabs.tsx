import { Tabs } from 'antd';
import type { ReactNode } from 'react';

/** Keep editors mounted when changing sections; an inactive tab never discards a draft. */
export function WorkspaceTabs({ items, activeKey, onChange, defaultActiveKey }: {
  items: { key: string; label: string; children: ReactNode }[];
  activeKey?: string;
  onChange?: (key: string) => void;
  defaultActiveKey?: string;
}) {
  return <Tabs className="page-tabs" activeKey={activeKey} onChange={onChange} defaultActiveKey={defaultActiveKey ?? items[0]?.key}
    destroyOnHidden={false} items={items.map(item => ({ ...item, forceRender: true, children: <div className="detail-tab-content">{item.children}</div> }))} />;
}
