import type { CSSProperties, ReactNode } from 'react';

import './Panel.css';

interface PanelProps {
  title?: ReactNode;
  extra?: ReactNode;
  inverted?: boolean;
  padded?: boolean;
  className?: string;
  style?: CSSProperties;
  children?: ReactNode;
}

export function Panel({
  title,
  extra,
  inverted = false,
  padded = true,
  className,
  style,
  children,
}: PanelProps) {
  const rootClass = [
    'aegis-panel',
    inverted ? 'aegis-panel--inverted' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');

  const showHeader = Boolean(title || extra);

  return (
    <section className={rootClass} style={style}>
      {showHeader && (
        <header className="aegis-panel__header">
          {typeof title === 'string' ? (
            <span className="aegis-panel__title">{title}</span>
          ) : (
            title
          )}
          {extra && <div className="aegis-panel__extra">{extra}</div>}
        </header>
      )}
      <div
        className={`aegis-panel__body${padded ? ' aegis-panel__body--padded' : ''}`}
      >
        {children}
      </div>
    </section>
  );
}

export default Panel;
