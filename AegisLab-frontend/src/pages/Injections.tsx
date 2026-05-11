import { useNavigate, useParams } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
  StatusDot,
} from '@/components/ui';

const DEMO_INJECTIONS = [
  { id: 'inj-001', name: 'clock-drift-01', status: 'completed' },
  { id: 'inj-002', name: 'network-loss-02', status: 'running' },
  { id: 'inj-003', name: 'cpu-stress-03', status: 'failed' },
];

export default function Injections() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Injections"
        description={`Fault injections for project ${projectId}.`}
        action={
          <Chip tone="ink" onClick={() => navigate(`/projects/${projectId}/injections/new`)}>+ New injection</Chip>
        }
      />
      <Panel>
        {DEMO_INJECTIONS.length === 0 ? (
          <EmptyState
            title="No injections"
            description="Injections will appear here once created."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">Status</span>
            </div>
            {DEMO_INJECTIONS.map((i) => (
              <div
                key={i.id}
                className="page-table__row"
                onClick={() => navigate(`/projects/${projectId}/injections/${i.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{i.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{i.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <StatusDot
                    size={6}
                    pulse={i.status === 'running'}
                    tone={
                      i.status === 'completed'
                        ? 'ink'
                        : i.status === 'running'
                          ? 'ink'
                          : 'warning'
                    }
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
