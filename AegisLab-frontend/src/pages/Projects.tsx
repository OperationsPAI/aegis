import { useNavigate } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
  StatusDot,
} from '@/components/ui';

const DEMO_PROJECTS = [
  { id: 'proj-catalog', name: 'catalog-service', status: 'active', injections: 12 },
  { id: 'proj-payment', name: 'payment-gateway', status: 'active', injections: 8 },
  { id: 'proj-auth', name: 'auth-service', status: 'archived', injections: 24 },
];

export default function Projects() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Projects"
        description="Manage fault-injection projects and their associated resources."
        action={
          <Chip tone="ink" onClick={() => navigate('/projects/new')}>
            + New project
          </Chip>
        }
      />
      <Panel>
        {DEMO_PROJECTS.length === 0 ? (
          <EmptyState
            title="No projects"
            description="Projects will appear here once created."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">Status</span>
              <span className="page-table__cell">Injections</span>
            </div>
            {DEMO_PROJECTS.map((p) => (
              <div
                key={p.id}
                className="page-table__row"
                onClick={() => navigate(`/projects/${p.id}/overview`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{p.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <StatusDot
                    size={6}
                    tone={p.status === 'active' ? 'ink' : 'muted'}
                  />
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{p.injections}</MonoValue>
                </span>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
