import { useNavigate, useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function InjectionCreate() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="New Injection"
        description={`Submit a fault injection for project ${projectId}.`}
        action={
          <button
            type="button"
            className="settings-demo-danger-btn"
            onClick={() => navigate(`/projects/${projectId}/injections`)}
          >
            Cancel
          </button>
        }
      />
      <Panel>
        <EmptyState
          title="Injection form"
          description="Fault injection submission form will appear here."
        />
      </Panel>
    </div>
  );
}
