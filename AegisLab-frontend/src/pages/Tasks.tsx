import { useNavigate } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
  StatusDot,
} from '@/components/ui';

const DEMO_TASKS = [
  { id: 'task-001', name: 'build-datapack', state: 'Completed' },
  { id: 'task-002', name: 'run-algorithm', state: 'Running' },
  { id: 'task-003', name: 'collect-results', state: 'Pending' },
];

export default function Tasks() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Tasks"
        description="Background jobs and task queue across all projects."
        action={
          <Chip tone="ink">View queue</Chip>
        }
      />
      <Panel>
        {DEMO_TASKS.length === 0 ? (
          <EmptyState
            title="No tasks"
            description="Tasks will appear here when background jobs are triggered."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Name</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">State</span>
            </div>
            {DEMO_TASKS.map((t) => (
              <div
                key={t.id}
                className="page-table__row"
                onClick={() => navigate(`/tasks/${t.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{t.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{t.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <StatusDot
                    size={6}
                    pulse={t.state === 'Running'}
                    tone={
                      t.state === 'Completed'
                        ? 'ink'
                        : t.state === 'Running'
                          ? 'ink'
                          : t.state === 'Failed'
                            ? 'warning'
                            : 'muted'
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
