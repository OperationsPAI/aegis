import { useNavigate, useParams } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
  StatusDot,
} from '@/components/ui';

const DEMO_EXECUTIONS = [
  { id: 'exec-001', name: 'rca-algo-v1', status: 'completed' },
  { id: 'exec-002', name: 'rca-algo-v2', status: 'running' },
  { id: 'exec-003', name: 'baseline-run', status: 'completed' },
];

export default function Executions() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Executions"
        description={`Algorithm executions for project ${projectId}.`}
        action={
          <Chip tone="ink" onClick={() => navigate(`/projects/${projectId}/executions/new`)}>+ Run algorithm</Chip>
        }
      />
      <Panel>
        {DEMO_EXECUTIONS.length === 0 ? (
          <EmptyState
            title="No executions"
            description="Executions will appear here once algorithms are run."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">Status</span>
            </div>
            {DEMO_EXECUTIONS.map((e) => (
              <div
                key={e.id}
                className="page-table__row"
                onClick={() => navigate(`/projects/${projectId}/executions/${e.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{e.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{e.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <StatusDot
                    size={6}
                    pulse={e.status === 'running'}
                    tone={
                      e.status === 'completed'
                        ? 'ink'
                        : e.status === 'running'
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
