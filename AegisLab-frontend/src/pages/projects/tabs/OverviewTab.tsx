import { GlobalOutlined, LockOutlined } from '@ant-design/icons';
import type { ProjectDetailResp } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Card, Col, Descriptions, Row, Statistic } from 'antd';
import dayjs from 'dayjs';

import { projectApi } from '@/api/projects';

/** Extended fields that may come from the API but are not in the SDK type */
type ProjectWithExtras = ProjectDetailResp & {
  team_name?: string;
  description?: string;
};

interface OverviewTabProps {
  project: ProjectDetailResp;
  projectId: number;
}

/**
 * Project overview tab showing basic info and quick stats.
 */
const OverviewTab: React.FC<OverviewTabProps> = ({
  project: rawProject,
  projectId,
}) => {
  const project = rawProject as ProjectWithExtras;
  // Fetch totals using size=1 to get counts without loading all data
  const { data: injectionsData } = useQuery({
    queryKey: ['project', projectId, 'injections', 'count'],
    queryFn: () =>
      projectApi.listProjectInjections(projectId, { page: 1, size: 1 }),
  });

  const { data: executionsData } = useQuery({
    queryKey: ['project', projectId, 'executions', 'count'],
    queryFn: () => projectApi.getExecutions(projectId, { page: 1, size: 1 }),
  });

  const datapackCount = injectionsData?.total ?? 0;
  const executionCount = executionsData?.pagination?.total ?? 0;

  return (
    <div>
      {/* Quick Stats */}
      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={8}>
          <Card>
            <Statistic title='Datapacks' value={datapackCount} />
          </Card>
        </Col>
        <Col span={8}>
          <Card>
            <Statistic title='Executions' value={executionCount} />
          </Card>
        </Col>
        <Col span={8}>
          <Card>
            <Statistic
              title='Visibility'
              value={project.is_public ? 'Public' : 'Private'}
              prefix={project.is_public ? <GlobalOutlined /> : <LockOutlined />}
            />
          </Card>
        </Col>
      </Row>

      {/* Project Info */}
      <Card title='Project Information'>
        <Descriptions column={2} bordered>
          <Descriptions.Item label='Name'>{project.name}</Descriptions.Item>
          <Descriptions.Item label='Team'>
            {project.team_name ?? '-'}
          </Descriptions.Item>
          <Descriptions.Item label='Description' span={2}>
            {project.description || 'No description'}
          </Descriptions.Item>
          <Descriptions.Item label='Created'>
            {project.created_at
              ? dayjs(project.created_at).format('YYYY-MM-DD HH:mm:ss')
              : '-'}
          </Descriptions.Item>
          <Descriptions.Item label='Last Updated'>
            {project.updated_at
              ? dayjs(project.updated_at).format('YYYY-MM-DD HH:mm:ss')
              : '-'}
          </Descriptions.Item>
        </Descriptions>
      </Card>
    </div>
  );
};

export default OverviewTab;
