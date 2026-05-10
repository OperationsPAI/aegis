import type { CSSProperties, ReactNode } from 'react';

import './ControlListItem.css';

interface ControlListItemProps {
  /** Left-side content (e.g. dot + label). */
  left: ReactNode;
  /** Right-side content (e.g. status text or action button). */
  right?: ReactNode;
  /** Inverts the row to ink — used for the currently-active item. */
  active?: boolean;
  /** Click target — when present, row becomes a button. */
  onClick?: () => void;
  className?: string;
  style?: CSSProperties;
}

export function ControlListItem({
  left,
  right,
  active = false,
  onClick,
  className,
  style,
}: ControlListItemProps) {
  const cls = [
    'aegis-control-item',
    active ? 'aegis-control-item--active' : '',
    onClick ? 'aegis-control-item--interactive' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');

  if (onClick) {
    return (
      <button type="button" className={cls} style={style} onClick={onClick}>
        <span className="aegis-control-item__left">{left}</span>
        {right && (
          <span className="aegis-control-item__right">{right}</span>
        )}
      </button>
    );
  }

  return (
    <div className={cls} style={style}>
      <span className="aegis-control-item__left">{left}</span>
      {right && <span className="aegis-control-item__right">{right}</span>}
    </div>
  );
}

export default ControlListItem;
