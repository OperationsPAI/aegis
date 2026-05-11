import { useNavigate } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
} from '@/components/ui';

const DEMO_DATASETS = [
  { id: 'ds-001', name: 'catalog-faults', versions: 3 },
  { id: 'ds-002', name: 'payment-traces', versions: 1 },
  { id: 'ds-003', name: 'auth-metrics', versions: 5 },
];

export default function Datasets() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Datasets"
        description="Manage evaluation datasets and their versions."
        action={
          <Chip tone="ink" onClick={() => navigate('/datasets/new')}>+ New dataset</Chip>
        }
      />
      <Panel>
        {DEMO_DATASETS.length === 0 ? (
          <EmptyState
            title="No datasets"
            description="Datasets will appear here once created."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">Versions</span>
            </div>
            {DEMO_DATASETS.map((d) => (
              <div
                key={d.id}
                className="page-table__row"
                onClick={() => navigate(`/datasets/${d.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{d.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{d.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{d.versions}</MonoValue>
                </span>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
