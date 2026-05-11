import { useNavigate, useParams } from 'react-router-dom';

import {
  Chip,
  EmptyState,
  MetricCard,
  PageHeader,
  Panel,
  PanelTitle,
} from '@/components/ui';

const QUICK_LINKS = [
  { label: 'Injections', to: 'injections', count: 12 },
  { label: 'Executions', to: 'executions', count: 8 },
  { label: 'Traces', to: 'traces', count: 34 },
  { label: 'Observations', to: 'observations', count: 5 },
];

export default function ProjectOverview() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Overview"
        description={`Project summary for ${projectId}.`}
        action={<Chip tone="ink">Edit project</Chip>}
      />

      <div className="page-overview-grid">
        {QUICK_LINKS.map((link) => (
          <MetricCard
            key={link.to}
            label={link.label}
            value={link.count}
            onClick={() => navigate(`/projects/${projectId}/${link.to}`)}
          />
        ))}
      </div>

      <Panel
        title={<PanelTitle size="base">Recent Activity</PanelTitle>}
      >
        <EmptyState
          title="Project overview"
          description="Project metrics and activity feed will appear here."
        />
      </Panel>
    </div>
  );
}
