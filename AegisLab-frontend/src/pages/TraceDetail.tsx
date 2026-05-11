import { useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function TraceDetail() {
  const { projectId, traceId } = useParams<{
    projectId: string;
    traceId: string;
  }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Trace ${traceId}`}
        description={`Distributed trace detail for project ${projectId}.`}
      />
      <Panel>
        <EmptyState
          title="Trace detail"
          description="Trace spans, service dependencies, and latency breakdown will appear here."
        />
      </Panel>
    </div>
  );
}
