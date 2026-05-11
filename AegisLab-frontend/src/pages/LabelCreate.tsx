import { useNavigate } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function LabelCreate() {
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="New Label"
        description="Create a custom label for organizing resources."
        action={
          <button
            type="button"
            className="settings-demo-danger-btn"
            onClick={() => navigate('/labels')}
          >
            Cancel
          </button>
        }
      />
      <Panel>
        <EmptyState
          title="Label form"
          description="Label creation form will appear here."
        />
      </Panel>
    </div>
  );
}
