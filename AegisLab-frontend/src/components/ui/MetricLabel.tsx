import type { CSSProperties, ReactNode } from 'react';

import './MetricLabel.css';

interface MetricLabelProps {
  children: ReactNode;
  as?: 'span' | 'div' | 'label';
  inverted?: boolean;
  size?: 'xs' | 'sm';
  className?: string;
  style?: CSSProperties;
}

export function MetricLabel({
  children,
  as: Tag = 'span',
  inverted = false,
  size = 'sm',
  className,
  style,
}: MetricLabelProps) {
  const cls = [
    'aegis-metric-label',
    `aegis-metric-label--${size}`,
    inverted ? 'aegis-metric-label--inverted' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <Tag className={cls} style={style}>
      {children}
    </Tag>
  );
}

export default MetricLabel;
