import type { ReactNode } from 'react';

import { PanelTitle } from './PanelTitle';
import { MetricLabel } from './MetricLabel';
import './PageHeader.css';

interface PageHeaderProps {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

export function PageHeader({ title, description, action, className }: PageHeaderProps) {
  const cls = ['aegis-page-header', className ?? ''].filter(Boolean).join(' ');

  return (
    <header className={cls}>
      <div className="aegis-page-header__text">
        <PanelTitle size="lg" as="h1">{title}</PanelTitle>
        {description && (
          <MetricLabel>{description}</MetricLabel>
        )}
      </div>
      {action && (
        <div className="aegis-page-header__action">{action}</div>
      )}
    </header>
  );
}

export default PageHeader;
