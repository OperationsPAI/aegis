import { useNavigate } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function DatasetCreate() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="New Dataset"
        description="Create a new evaluation dataset."
        action={
          <button
            type="button"
            className="settings-demo-danger-btn"
            onClick={() => navigate('/datasets')}
          >
            Cancel
          </button>
        }
      />
      <Panel>
        <EmptyState
          title="Dataset form"
          description="Dataset creation form will appear here."
        />
      </Panel>
    </div>
  );
}
