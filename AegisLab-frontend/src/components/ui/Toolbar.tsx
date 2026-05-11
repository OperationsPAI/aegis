import { SearchOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';

import { Chip } from './Chip';
import './Toolbar.css';

export interface FilterChip {
  key: string;
  label: string;
}

interface ToolbarProps {
  searchPlaceholder?: string;
  searchValue?: string;
  onSearchChange?: (value: string) => void;
  filters?: FilterChip[];
  onFilterRemove?: (key: string) => void;
  onClearFilters?: () => void;
  action?: ReactNode;
  className?: string;
}

export function Toolbar({
  searchPlaceholder = 'Search…',
  searchValue = '',
  onSearchChange,
  filters = [],
  onFilterRemove,
  onClearFilters,
  action,
  className,
}: ToolbarProps) {
  const hasFilters = filters.length > 0;

  const cls = ['aegis-toolbar', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-toolbar__left">
        <div className="aegis-toolbar__search">
          <SearchOutlined className="aegis-toolbar__search-icon" />
          <input
            type="text"
            className="aegis-toolbar__search-input"
            placeholder={searchPlaceholder}
            value={searchValue}
            onChange={(e) => onSearchChange?.(e.target.value)}
            aria-label="Search"
          />
        </div>

        {hasFilters && (
          <div className="aegis-toolbar__filters">
            {filters.map((f) => (
              <button
                key={f.key}
                type="button"
                className="aegis-toolbar__filter-btn"
                onClick={() => onFilterRemove?.(f.key)}
                title={`Remove filter ${f.label}`}
              >
                <Chip tone="default">{f.label}</Chip>
                <span className="aegis-toolbar__filter-close">×</span>
              </button>
            ))}
            {onClearFilters && (
              <button
                type="button"
                className="aegis-toolbar__clear"
                onClick={onClearFilters}
              >
                Clear all
              </button>
            )}
          </div>
        )}
      </div>

      {action && (
        <div className="aegis-toolbar__right">{action}</div>
      )}
    </div>
  );
}

export default Toolbar;
