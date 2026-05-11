import { useParams } from 'react-router-dom';

import { EmptyState, PageHeader, Panel } from '@/components/ui';

export default function LabelDetail() {
  const { labelId } = useParams<{ labelId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title={`Label ${labelId}`}
        description="Label details and associated resources."
      />
      <Panel>
        <EmptyState
          title="Label detail"
          description="Label metadata and tagged resources will appear here."
        />
      </Panel>
    </div>
  );
}
