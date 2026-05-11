import { useNavigate } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
} from '@/components/ui';

const DEMO_LABELS = [
  { id: 'lbl-001', name: 'critical', color: '#E11D48' },
  { id: 'lbl-002', name: 'staging', color: '#3B82F6' },
  { id: 'lbl-003', name: 'production', color: '#10B981' },
];

export default function Labels() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Labels"
        description="Organize and filter resources with custom labels."
        action={
          <Chip tone="ink" onClick={() => navigate('/labels/new')}>+ New label</Chip>
        }
      />
      <Panel>
        {DEMO_LABELS.length === 0 ? (
          <EmptyState
            title="No labels"
            description="Labels will appear here once created."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">Color</span>
            </div>
            {DEMO_LABELS.map((l) => (
              <div
                key={l.id}
                className="page-table__row"
                onClick={() => navigate(`/labels/${l.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{l.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{l.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <span
                    style={{
                      display: 'inline-block',
                      width: 12,
                      height: 12,
                      borderRadius: 'var(--radius-circle)',
                      background: l.color,
                    }}
                  />
                </span>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
