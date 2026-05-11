import { useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function InjectionDetail() {
  const { projectId, injectionId } = useParams<{
    projectId: string;
    injectionId: string;
  }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Injection ${injectionId}`}
        description={`Fault injection detail for project ${projectId}.`}
      />
      <Panel>
        <EmptyState
          title="Injection detail"
          description="Injection metadata, datapack files, and results will appear here."
        />
      </Panel>
    </div>
  );
}
