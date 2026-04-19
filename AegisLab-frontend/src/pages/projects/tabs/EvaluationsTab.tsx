import { BarChartOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Card, Empty, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { evaluationApi } from '@/api/evaluations';

const { Text } = Typography;

interface EvaluationsTabProps {
  projectId: number;
}

interface EvaluationRow {
  id?: number;
  datapack_id?: number;
  algorithm_names?: string[];
  status?: string;
  created_at?: string;
}

/**
 * Evaluations tab - shows existing evaluations and a placeholder for future comparison UI.
 */
const EvaluationsTab: React.FC<EvaluationsTabProps> = ({
  projectId: _projectId,
}) => {
  const { data, isLoading } = useQuery({
    queryKey: ['evaluations'],
    queryFn: () => evaluationApi.getEvaluations({ page: 1, size: 20 }),
  });

  const columns: ColumnsType<EvaluationRow> = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
    },
    {
      title: 'Datapack',
      dataIndex: 'datapack_id',
      key: 'datapack_id',
    },
    {
      title: 'Algorithms',
      dataIndex: 'algorithm_names',
      key: 'algorithm_names',
      render: (names: string[] | undefined) => names?.join(', ') ?? '-',
    },
  ];

  const items = (data?.items ?? []) as EvaluationRow[];

  return (
    <div>
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
            pagination={false}
          />
        </Card>
      )}
    </div>
  );
};

export default EvaluationsTab;
