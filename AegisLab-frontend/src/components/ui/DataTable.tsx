import type { ReactNode } from 'react';

import { EmptyState } from './EmptyState';
import './DataTable.css';

export interface DataTableColumn<T> {
  key: string;
  header: ReactNode;
  width?: string;
  align?: 'left' | 'center' | 'right';
  render: (row: T, index: number) => ReactNode;
}

interface DataTableProps<T> {
  columns: Array<DataTableColumn<T>>;
  data: T[];
  rowKey: (row: T, index: number) => string | number;
  loading?: boolean;
  emptyTitle?: string;
  emptyDescription?: string;
  emptyAction?: ReactNode;
  className?: string;
}

export function DataTable<T>({
  columns,
  data,
  rowKey,
  loading = false,
  emptyTitle = 'No data',
  emptyDescription,
  emptyAction,
  className,
}: DataTableProps<T>) {
  const cls = ['aegis-data-table', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-data-table__scroll">
        <table className="aegis-data-table__table">
          <thead>
            <tr>
              {columns.map((col) => (
                <th
                  key={col.key}
                  className="aegis-data-table__th"
                  style={{
                    width: col.width,
                    textAlign: col.align ?? 'left',
                  }}
                >
                  {col.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <tr key={`sk-${i}`} className="aegis-data-table__row">
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      className="aegis-data-table__td"
                      style={{ textAlign: col.align ?? 'left' }}
                    >
                      <span className="aegis-data-table__skeleton" />
                    </td>
                  ))}
                </tr>
              ))
            ) : data.length === 0 ? (
              <tr>
                <td
                  colSpan={columns.length}
                  className="aegis-data-table__empty-cell"
                >
                  <EmptyState
                    title={emptyTitle}
                    description={emptyDescription}
                    action={emptyAction}
                  />
                </td>
              </tr>
            ) : (
              data.map((row, idx) => (
                <tr key={rowKey(row, idx)} className="aegis-data-table__row">
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      className="aegis-data-table__td"
                      style={{ textAlign: col.align ?? 'left' }}
                    >
                      {col.render(row, idx)}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default DataTable;
