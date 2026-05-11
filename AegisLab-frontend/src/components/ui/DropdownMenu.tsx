import { useEffect, useRef, useState, type ReactNode } from 'react';

import './DropdownMenu.css';

export interface DropdownItem {
  key: string;
  label: ReactNode;
  onClick?: () => void;
  icon?: ReactNode;
  danger?: boolean;
  disabled?: boolean;
}

interface DropdownMenuProps {
  trigger: ReactNode;
  items: DropdownItem[];
  className?: string;
  align?: 'left' | 'right';
}

export function DropdownMenu({
  trigger,
  items,
  className,
  align = 'right',
}: DropdownMenuProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }

    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  const handleItemClick = (item: DropdownItem) => {
    if (item.disabled) return;
    item.onClick?.();
    setOpen(false);
  };

  const cls = [
    'aegis-dropdown',
    className ?? '',
  ].filter(Boolean).join(' ');

  const panelCls = [
    'aegis-dropdown__panel',
    `aegis-dropdown__panel--${align}`,
    open ? 'aegis-dropdown__panel--open' : '',
  ].filter(Boolean).join(' ');

  return (
    <div className={cls} ref={ref}>
      <button
        type="button"
        className="aegis-dropdown__trigger"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        {trigger}
      </button>
      <div className={panelCls}>
        {items.map((item) => (
          <button
            key={item.key}
            type="button"
            className={[
              'aegis-dropdown__item',
              item.danger ? 'aegis-dropdown__item--danger' : '',
              item.disabled ? 'aegis-dropdown__item--disabled' : '',
            ].filter(Boolean).join(' ')}
            onClick={() => handleItemClick(item)}
            disabled={item.disabled}
          >
            {item.icon && (
              <span className="aegis-dropdown__item-icon">{item.icon}</span>
            )}
            <span className="aegis-dropdown__item-label">{item.label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

export default DropdownMenu;
