import type { ReactNode } from 'react';

import { MetricLabel } from './MetricLabel';
import { MonoValue } from './MonoValue';
import { SparkLine } from './SparkLine';
import './MetricCard.css';

interface MetricCardProps {
  label: ReactNode;
  value: ReactNode;
  /** Optional small unit appended after the main value. */
  unit?: ReactNode;
  /** Optional sparkline samples — drawn beneath the value. */
  sparkline?: number[];
  /** Sparkline-only height in px. */
  sparklineHeight?: number;
  inverted?: boolean;
  className?: string;
}

export function MetricCard({
  label,
  value,
  unit,
  sparkline,
  sparklineHeight = 60,
  inverted = false,
  className,
}: MetricCardProps) {
  const cls = [
    'aegis-metric-card',
    inverted ? 'aegis-metric-card--inverted' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <div className={cls}>
      <MetricLabel inverted={inverted}>{label}</MetricLabel>
      <div className="aegis-metric-card__value">
        <MonoValue size="lg" inverted={inverted}>
          {value}
        </MonoValue>
        {unit && (
          <MetricLabel inverted={inverted} className="aegis-metric-card__unit">
            {unit}
          </MetricLabel>
        )}
      </div>
      {sparkline && sparkline.length > 0 && (
        <div
          className="aegis-metric-card__chart"
          style={{ height: sparklineHeight }}
        >
          <SparkLine points={sparkline} inverted={inverted} />
        </div>
      )}
    </div>
  );
}

export default MetricCard;
