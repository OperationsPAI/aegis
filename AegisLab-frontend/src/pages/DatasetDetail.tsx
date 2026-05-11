import { useParams } from 'react-router-dom';

import { Chip, EmptyState, PageHeader, Panel } from '@/components/ui';

export default function DatasetDetail() {
  const { datasetId } = useParams<{ datasetId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Dataset ${datasetId}`}
        description="Dataset versions and associated injections."
        action={<Chip tone="ink">+ New version</Chip>}
      />
      <Panel>
        <EmptyState
          title="Dataset detail"
          description="Dataset metadata, version history, and injection mappings will appear here."
        />
      </Panel>
    </div>
  );
}
