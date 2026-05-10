import type { ReactNode } from 'react';

import './Chip.css';

interface ChipProps {
  children: ReactNode;
  /** Visual treatment. */
  tone?: 'default' | 'ink' | 'warning' | 'ghost';
  /** Optional leading dot/icon node. */
  leading?: ReactNode;
  className?: string;
}

export function Chip({
  children,
  tone = 'default',
  leading,
  className,
}: ChipProps) {
  const cls = ['aegis-chip', `aegis-chip--${tone}`, className ?? '']
    .filter(Boolean)
    .join(' ');
  return (
    <span className={cls}>
      {leading && <span className="aegis-chip__leading">{leading}</span>}
      <span className="aegis-chip__label">{children}</span>
    </span>
  );
}

export default Chip;
