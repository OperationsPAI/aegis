import type { ReactNode } from 'react';

import './EmptyState.css';

interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: EmptyStateProps) {
  const cls = ['aegis-empty-state', className ?? ''].filter(Boolean).join(' ');
  return (
    <div className={cls}>
      {icon && <div className="aegis-empty-state__icon">{icon}</div>}
      <div className="aegis-empty-state__title">{title}</div>
      {description && (
        <div className="aegis-empty-state__desc">{description}</div>
      )}
      {action && <div className="aegis-empty-state__action">{action}</div>}
    </div>
  );
}

export default EmptyState;
