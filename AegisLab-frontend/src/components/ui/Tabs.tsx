import { useState, type ReactNode } from 'react';

import './Tabs.css';

export interface TabItem {
  key: string;
  label: ReactNode;
}

interface TabsProps {
  items: TabItem[];
  activeKey?: string;
  defaultActiveKey?: string;
  onChange?: (key: string) => void;
  children?: ReactNode;
  className?: string;
}

export function Tabs({
  items,
  activeKey: controlledKey,
  defaultActiveKey,
  onChange,
  children,
  className,
}: TabsProps) {
  const [internalKey, setInternalKey] = useState(
    defaultActiveKey ?? items[0]?.key,
  );
  const activeKey = controlledKey ?? internalKey;

  const handleClick = (key: string) => {
    if (controlledKey === undefined) {
      setInternalKey(key);
    }
    onChange?.(key);
  };

  const cls = ['aegis-tabs', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-tabs__list" role="tablist">
        {items.map((item) => {
          const isActive = item.key === activeKey;
          return (
            <button
              key={item.key}
              type="button"
              role="tab"
              aria-selected={isActive}
              className={
                isActive
                  ? 'aegis-tabs__tab aegis-tabs__tab--active'
                  : 'aegis-tabs__tab'
              }
              onClick={() => handleClick(item.key)}
            >
              {item.label}
            </button>
          );
        })}
      </div>
      <div className="aegis-tabs__panel" role="tabpanel">
        {children}
      </div>
    </div>
  );
}

export default Tabs;
