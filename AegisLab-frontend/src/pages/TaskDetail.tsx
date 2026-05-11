import { useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function TaskDetail() {
  const { taskId } = useParams<{ taskId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Task ${taskId}`}
        description="Task execution logs and status."
      />
      <Panel>
        <EmptyState
          title="Task detail"
          description="Task logs, execution timeline, and output will appear here."
        />
      </Panel>
    </div>
  );
}
