import { Link } from 'react-router-dom';
import { type ReactNode } from 'react';

import './Breadcrumb.css';

export interface BreadcrumbItem {
  label: ReactNode;
  to?: string;
}

interface BreadcrumbProps {
  items: BreadcrumbItem[];
  className?: string;
}

export function Breadcrumb({ items, className }: BreadcrumbProps) {
  const cls = ['aegis-breadcrumb', className ?? ''].filter(Boolean).join(' ');

  return (
    <nav aria-label="Breadcrumb" className={cls}>
      <ol className="aegis-breadcrumb__list">
        {items.map((item, idx) => {
          const isLast = idx === items.length - 1;
          return (
            <li key={idx} className="aegis-breadcrumb__item">
              {item.to && !isLast ? (
                <Link
                  to={item.to}
                  className="aegis-breadcrumb__link"
                >
                  {item.label}
                </Link>
              ) : (
                <span
                  className={
                    isLast
                      ? 'aegis-breadcrumb__text aegis-breadcrumb__text--current'
                      : 'aegis-breadcrumb__text'
                  }
                  aria-current={isLast ? 'page' : undefined}
                >
                  {item.label}
                </span>
              )}
              {!isLast && (
                <span className="aegis-breadcrumb__sep" aria-hidden="true">
                  /
                </span>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

export default Breadcrumb;
