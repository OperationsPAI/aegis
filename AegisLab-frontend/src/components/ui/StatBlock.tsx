import type { ReactNode } from 'react';

import { MetricLabel } from './MetricLabel';
import { MonoValue } from './MonoValue';
import './StatBlock.css';

interface StatBlockProps {
  label: ReactNode;
  value: ReactNode;
  /** Optional unit displayed after the value. */
  unit?: ReactNode;
  /** Layout direction. Default: horizontal (label · value, baseline). */
  direction?: 'horizontal' | 'vertical';
  /** Use the larger 18 px mono variant. */
  emphasized?: boolean;
  inverted?: boolean;
  className?: string;
}

export function StatBlock({
  label,
  value,
  unit,
  direction = 'horizontal',
  emphasized = false,
  inverted = false,
  className,
}: StatBlockProps) {
  const cls = [
    'aegis-stat',
    `aegis-stat--${direction}`,
    inverted ? 'aegis-stat--inverted' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <div className={cls}>
      <MetricLabel inverted={inverted} className="aegis-stat__label">
        {label}
      </MetricLabel>
      <span className="aegis-stat__value">
        <MonoValue size={emphasized ? 'lg' : 'sm'} inverted={inverted}>
          {value}
        </MonoValue>
        {unit && (
          <span className="aegis-stat__unit">
            <MetricLabel inverted={inverted}>{unit}</MetricLabel>
          </span>
        )}
      </span>
    </div>
  );
}

export default StatBlock;
