import { useNavigate } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
  StatusDot,
} from '@/components/ui';

const DEMO_CONTAINERS = [
  { id: 'ctn-catalog', name: 'catalog-service', status: 'active', type: 'benchmark' },
  { id: 'ctn-payment', name: 'payment-gateway', status: 'active', type: 'pedestal' },
  { id: 'ctn-auth', name: 'auth-service', status: 'inactive', type: 'algorithm' },
];

export default function Containers() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Containers"
        description="Manage benchmark, pedestal, and algorithm containers."
        action={
          <Chip tone="ink" onClick={() => navigate('/containers/new')}>+ Register container</Chip>
        }
      />
      <Panel>
        {DEMO_CONTAINERS.length === 0 ? (
          <EmptyState
            title="No containers"
            description="Containers will appear here once registered."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">Type</span>
              <span className="page-table__cell">Status</span>
            </div>
            {DEMO_CONTAINERS.map((c) => (
              <div
                key={c.id}
                className="page-table__row"
                onClick={() => navigate(`/containers/${c.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{c.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{c.type}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <StatusDot
                    size={6}
                    tone={c.status === 'active' ? 'ink' : 'muted'}
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
