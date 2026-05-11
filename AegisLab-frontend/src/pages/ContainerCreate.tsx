import { useNavigate } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function ContainerCreate() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Register Container"
        description="Register a new container, version, and helm configuration."
        action={
          <button
            type="button"
            className="settings-demo-danger-btn"
            onClick={() => navigate('/containers')}
          >
            Cancel
          </button>
        }
      />
      <Panel>
        <EmptyState
          title="Container form"
          description="Container registration form will appear here."
        />
      </Panel>
    </div>
  );
}
