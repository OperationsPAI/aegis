import { useNavigate, useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function ExecutionCreate() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Run Algorithm"
        description={`Submit an algorithm execution for project ${projectId}.`}
        action={
          <button
            type="button"
            className="settings-demo-danger-btn"
            onClick={() => navigate(`/projects/${projectId}/executions`)}
          >
            Cancel
          </button>
        }
      />
      <Panel>
        <EmptyState
          title="Execution form"
          description="Algorithm execution submission form will appear here."
        />
      </Panel>
    </div>
  );
}
