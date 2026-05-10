import type { ReactNode } from 'react';

import './SectionDivider.css';

interface SectionDividerProps {
  /** Uppercase tracked label. */
  children: ReactNode;
  /** Right-aligned content (chip / link / mono value). */
  extra?: ReactNode;
  /** Render the hairline rule below the label. */
  rule?: boolean;
  className?: string;
}

export function SectionDivider({
  children,
  extra,
  rule = true,
  className,
}: SectionDividerProps) {
  const cls = [
    'aegis-section-divider',
    rule ? 'aegis-section-divider--ruled' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={cls}>
      <div className="aegis-section-divider__head">
        <span className="aegis-section-divider__label">{children}</span>
        {extra && <span className="aegis-section-divider__extra">{extra}</span>}
      </div>
    </div>
  );
}

export default SectionDivider;
