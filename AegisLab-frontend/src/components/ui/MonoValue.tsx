import type { CSSProperties, ReactNode } from 'react';

import './MonoValue.css';

interface MonoValueProps {
  children: ReactNode;
  size?: 'sm' | 'base' | 'lg';
  weight?: 'regular' | 'medium';
  inverted?: boolean;
  as?: 'span' | 'div';
  className?: string;
  style?: CSSProperties;
}

export function MonoValue({
  children,
  size = 'base',
  weight = 'medium',
  inverted = false,
  as: Tag = 'span',
  className,
  style,
}: MonoValueProps) {
  const cls = [
    'aegis-mono',
    `aegis-mono--${size}`,
    `aegis-mono--${weight}`,
    inverted ? 'aegis-mono--inverted' : '',
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

export default MonoValue;
