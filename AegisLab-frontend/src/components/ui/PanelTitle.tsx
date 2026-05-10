import type { ReactNode } from 'react';

import './PanelTitle.css';

interface PanelTitleProps {
  children: ReactNode;
  as?: 'span' | 'h1' | 'h2' | 'h3' | 'h4';
  italic?: boolean;
  size?: 'sm' | 'base' | 'lg' | 'hero';
  className?: string;
}

const sizeClass: Record<NonNullable<PanelTitleProps['size']>, string> = {
  sm: 'aegis-panel-title--sm',
  base: 'aegis-panel-title--base',
  lg: 'aegis-panel-title--lg',
  hero: 'aegis-panel-title--hero',
};

export function PanelTitle({
  children,
  as: Tag = 'span',
  italic = false,
  size = 'base',
  className,
}: PanelTitleProps) {
  const cls = [
    'aegis-panel-title',
    italic ? 'aegis-panel-title--italic' : '',
    sizeClass[size],
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');

  return <Tag className={cls}>{children}</Tag>;
}

export default PanelTitle;
