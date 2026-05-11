import { useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function ExecutionDetail() {
  const { projectId, executionId } = useParams<{
    projectId: string;
    executionId: string;
  }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Execution ${executionId}`}
        description={`Algorithm execution detail for project ${projectId}.`}
      />
      <Panel>
        <EmptyState
          title="Execution detail"
          description="Execution results, detector output, and evaluation scores will appear here."
        />
      </Panel>
    </div>
  );
}
