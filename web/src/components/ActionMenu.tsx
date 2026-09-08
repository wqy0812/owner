import { Button, Dropdown } from 'antd';
import { MoreHorizontal } from 'lucide-react';
import type { ReactNode } from 'react';

export interface ActionMenuItem {
  key: string;
  label: ReactNode;
  disabled?: boolean;
  danger?: boolean;
  onClick: () => unknown;
}
export function ActionMenu({ items, ariaLabel = '更多操作' }: { ariaLabel?: string; items: (ActionMenuItem | false | null | undefined)[] }) {
  const visible = items.filter((item): item is ActionMenuItem => Boolean(item));
  if (!visible.length) return null;
  return <Dropdown trigger={['click']} menu={{ items: visible.map(item => ({ ...item, onClick: () => { void item.onClick(); } })) }}>
    <Button icon={<MoreHorizontal size={16} />} aria-label={ariaLabel}>更多操作</Button>
  </Dropdown>;
}
