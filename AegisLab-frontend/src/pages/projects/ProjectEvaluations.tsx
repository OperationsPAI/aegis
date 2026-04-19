import { useState } from 'react';
import { useParams } from 'react-router-dom';

import { BarChartOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Card, Empty, Skeleton, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { evaluationApi } from '@/api/evaluations';
import { projectApi } from '@/api/projects';

import ProjectSubNav from './ProjectSubNav';

const { Text } = Typography;

interface EvaluationRow {
  id?: number;
  datapack_id?: number;
  algorithm_names?: string[];
  status?: string;
  created_at?: string;
}

/**
 * Evaluations page for a project — comparison of algorithm results.
 * Route: /projects/:id/evaluations
 */
const ProjectEvaluations: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const projectId = Number(id);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);

  const { isLoading: projectLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => projectApi.getProjectDetail(projectId),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  const { data, isLoading } = useQuery({
    queryKey: ['evaluations', projectId, page, pageSize],
    queryFn: () => evaluationApi.getEvaluations({ page, size: pageSize }),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  if (projectLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    );
  }

  const columns: ColumnsType<EvaluationRow> = [
    { title: 'ID', dataIndex: 'id', key: 'id' },
    { title: 'Datapack', dataIndex: 'datapack_id', key: 'datapack_id' },
    {
      title: 'Algorithms',
      dataIndex: 'algorithm_names',
      key: 'algorithm_names',
      render: (names: string[] | undefined) => names?.join(', ') ?? '-',
    },
  ];

  const items = (data?.items ?? []) as EvaluationRow[];

  return (
    <div style={{ padding: 24 }}>
      <ProjectSubNav projectId={projectId} activeKey='evaluations' />

      <Card style={{ marginBottom: 24, textAlign: 'center' }}>
        <Empty
          image={
            <BarChartOutlined
              style={{ fontSize: 48, color: 'var(--color-secondary-300)' }}
            />
          }
          description={
            <div>
              <Text strong>Select datapacks and algorithms to compare</Text>
              <br />
              <Text type='secondary'>
                Evaluation comparison will be available here once you have
                datapacks and algorithm results to compare.
              </Text>
            </div>
          }
        />
      </Card>

      {items.length > 0 && (
        <Card title='Existing Evaluations'>
          <Table
            columns={columns}
            dataSource={items}
            rowKey='id'
            loading={isLoading}
            pagination={{
              current: page,
              pageSize,
              total: data?.total ?? items.length,
              showSizeChanger: true,
              showTotal: (t) => `Total ${t} evaluations`,
              onChange: (p, s) => {
                setPage(p);
                setPageSize(s);
              },
            }}
          />
        </Card>
      )}
    </div>
  );
};

export default ProjectEvaluations;
