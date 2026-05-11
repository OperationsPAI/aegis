import { useParams } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  PageHeader,
  Panel,
} from '@/components/ui';

export default function Observations() {
  const { projectId } = useParams<{ projectId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Observations"
        description={`Data collection sources for project ${projectId}.`}
        action={<Chip tone="ink">+ Add source</Chip>}
      />
      <Panel>
        <EmptyState
          title="No observations"
          description="Observation sources will appear here once added."
        />
      </Panel>
    </div>
  );
}
