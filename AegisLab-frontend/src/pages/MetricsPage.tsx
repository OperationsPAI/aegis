import { useParams } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  PageHeader,
  Panel,
} from '@/components/ui';

export default function MetricsPage() {
  const { projectId } = useParams<{ projectId: string }>();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Metrics"
        description={`Performance and reliability metrics for project ${projectId}.`}
        action={<Chip tone="ink">Export</Chip>}
      />
      <Panel>
        <EmptyState
          title="No metrics"
          description="Metrics will appear here once data is collected."
        />
      </Panel>
    </div>
  );
}
