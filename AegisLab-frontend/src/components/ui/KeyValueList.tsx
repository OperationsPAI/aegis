import type { ReactNode } from 'react';

import { MetricLabel } from './MetricLabel';
import { MonoValue } from './MonoValue';
import './KeyValueList.css';

export interface KeyValueItem {
  /** Left key (mono — IDs, fields). */
  k: ReactNode;
  /** Right value (mono number / string). */
  v: ReactNode;
}

interface KeyValueListProps {
  items: KeyValueItem[];
  /** Render keys as uppercase tracked labels instead of mono. */
  uppercaseKeys?: boolean;
  /** Top hairline above the first row. */
  ruled?: boolean;
  className?: string;
}

export function KeyValueList({
  items,
  uppercaseKeys = false,
  ruled = true,
  className,
}: KeyValueListProps) {
  const cls = [
    'aegis-kv',
    ruled ? 'aegis-kv--ruled' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <dl className={cls}>
      {items.map((item, i) => (
        <div className="aegis-kv__row" key={i}>
          <dt className="aegis-kv__k">
            {uppercaseKeys ? (
              <MetricLabel>{item.k}</MetricLabel>
            ) : (
              <MonoValue size="sm" weight="regular">
                {item.k}
              </MonoValue>
            )}
          </dt>
          <dd className="aegis-kv__v">
            <MonoValue size="sm">{item.v}</MonoValue>
          </dd>
        </div>
      ))}
    </dl>
  );
}

export default KeyValueList;
