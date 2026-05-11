import { useNavigate, useParams } from 'react-router-dom';

import {
  EmptyState,
  MonoValue,
  PageHeader,
  Panel,
} from '@/components/ui';

const DEMO_TRACES = [
  { id: 'trace-001', name: 'GET /products', duration: '2.84s' },
  { id: 'trace-002', name: 'POST /checkout', duration: '1.21s' },
  { id: 'trace-003', name: 'GET /user/profile', duration: '0.45s' },
];

export default function Traces() {
  const { projectId } = useParams<{ projectId: string }>();
  const navigate = useNavigate();

  return (
    <div className="page-wrapper">
      <PageHeader
        title="Traces"
        description={`Distributed traces for project ${projectId}.`}
      />
      <Panel>
        {DEMO_TRACES.length === 0 ? (
          <EmptyState
            title="No traces"
            description="Traces will appear here once data is collected."
          />
        ) : (
          <div className="page-table">
            <div className="page-table__head">
              <span className="page-table__cell">Operation</span>
              <span className="page-table__cell">ID</span>
              <span className="page-table__cell">Duration</span>
            </div>
            {DEMO_TRACES.map((t) => (
              <div
                key={t.id}
                className="page-table__row"
                onClick={() => navigate(`/projects/${projectId}/traces/${t.id}`)}
              >
                <span className="page-table__cell">
                  <MonoValue size="sm">{t.name}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{t.id}</MonoValue>
                </span>
                <span className="page-table__cell">
                  <MonoValue size="sm">{t.duration}</MonoValue>
                </span>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
