import { useParams } from 'react-router-dom';

import { Chip, EmptyState, PageHeader, Panel } from '@/components/ui';

export default function ContainerDetail() {
  const { containerId } = useParams<{ containerId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Container ${containerId}`}
        description="Container configuration and version history."
        action={<Chip tone="ink">+ New version</Chip>}
      />
      <Panel>
        <EmptyState
          title="Container detail"
          description="Container metadata, versions, and helm charts will appear here."
        />
      </Panel>
    </div>
  );
}
