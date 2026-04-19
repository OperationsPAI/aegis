import { Link, useNavigate, useParams } from 'react-router-dom';

import {
  BarChartOutlined,
  PlayCircleOutlined,
  PlusOutlined,
  RightOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import type {
  ExecutionResp,
  InjectionResp,
  ProjectDetailResp,
} from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Card,
  Col,
  Row,
  Skeleton,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

import { projectApi } from '@/api/projects';

import ProjectSubNav from './ProjectSubNav';
import { executionStateMap, injectionStateMap } from './stateLabels';

const { Title, Text } = Typography;

type ProjectWithExtras = ProjectDetailResp & {
  description?: string;
};

/**
 * Project dashboard page — single scrollable overview of the experiment state.
 * Route: /projects/:id
 */
const ProjectDetail: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const projectId = Number(id);
  const navigate = useNavigate();

  const {
    data: project,
    isLoading,
    error,
  } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => projectApi.getProjectDetail(projectId),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  const { data: injectionsData } = useQuery({
    queryKey: ['project', projectId, 'injections', 'recent'],
    queryFn: () =>
      projectApi.listProjectInjections(projectId, { page: 1, size: 10 }),
    enabled: !!projectId && !Number.isNaN(projectId) && !!project,
  });

  const { data: executionsData } = useQuery({
    queryKey: ['project', projectId, 'executions', 'recent'],
    queryFn: () => projectApi.getExecutions(projectId, { page: 1, size: 10 }),
    enabled: !!projectId && !Number.isNaN(projectId) && !!project,
  });

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }

  if (error || !project) {
    return (
      <div style={{ padding: 24 }}>
        <Title level={4}>Project not found</Title>
      </div>
    );
  }

  const datapacks = injectionsData?.items ?? [];
  const datapackTotal = injectionsData?.total ?? 0;
  const executions = executionsData?.items ?? [];
  const executionTotal = executionsData?.pagination?.total ?? 0;

  // Compute pipeline status dots
  const datapacksByState = datapacks.reduce<Record<string, number>>(
    (acc, dp) => {
      const stateKey = Number(dp.state ?? 0);
      const mapping = injectionStateMap[stateKey] ?? {
        label: 'Unknown',
        color: 'default',
      };
      acc[mapping.color] = (acc[mapping.color] ?? 0) + 1;
      return acc;
    },
    {}
  );

  const executionsByState = executions.reduce<Record<string, number>>(
    (acc, ex) => {
      const stateKey = Number(ex.state ?? 0);
      const mapping = executionStateMap[stateKey] ?? {
        label: 'Unknown',
        color: 'default',
      };
      acc[mapping.color] = (acc[mapping.color] ?? 0) + 1;
      return acc;
    },
    {}
  );

  const datapackColumns: ColumnsType<InjectionResp> = [
    { title: 'Name', dataIndex: 'name', key: 'name' },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      render: (state: number) => {
        const mapping = injectionStateMap[state] ?? {
          label: 'Unknown',
          color: 'default',
        };
        return <Tag color={mapping.color}>{mapping.label}</Tag>;
      },
    },
    { title: 'Fault Type', dataIndex: 'fault_type', key: 'fault_type' },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) =>
        date ? dayjs(date).format('YYYY-MM-DD HH:mm') : '-',
    },
  ];

  const executionColumns: ColumnsType<ExecutionResp> = [
    {
      title: 'Algorithm',
      dataIndex: 'algorithm_name',
      key: 'algorithm_name',
      render: (name: string, record) => (
        <span>
          {name ?? '-'}
          {record.algorithm_version ? ` (${record.algorithm_version})` : ''}
        </span>
      ),
    },
    {
      title: 'Datapack',
      dataIndex: 'datapack_name',
      key: 'datapack_name',
      render: (name: string) => name ?? '-',
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      render: (state: number) => {
        const mapping = executionStateMap[state] ?? {
          label: 'Unknown',
          color: 'default',
        };
        return <Tag color={mapping.color}>{mapping.label}</Tag>;
      },
    },
    {
      title: 'Duration',
      dataIndex: 'duration',
      key: 'duration',
      render: (duration: number | undefined) => {
        if (duration == null) return '-';
        if (duration < 60) return `${duration}s`;
        const minutes = Math.floor(duration / 60);
        const seconds = duration % 60;
        return `${minutes}m ${seconds}s`;
      },
    },
  ];

  const renderStatusDots = (byState: Record<string, number>) => {
    const colorMap: Record<string, string> = {
      green: '#52c41a',
      red: '#ff4d4f',
      blue: '#1677ff',
      default: '#d9d9d9',
    };
    return (
      <Space size={2} wrap>
        {Object.entries(byState).map(([color, count]) =>
          Array.from({ length: Math.min(count, 20) }).map((_, i) => (
            <span
              key={`${color}-${i}`}
              style={{
                display: 'inline-block',
                width: 8,
                height: 8,
                borderRadius: '50%',
                backgroundColor: colorMap[color] ?? colorMap.default,
              }}
            />
          ))
        )}
      </Space>
    );
  };

  return (
    <div style={{ padding: 24 }}>
      {/* Breadcrumb + Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          marginBottom: 16,
        }}
      >
        <div>
          {(project as ProjectWithExtras).description ? (
            <Text type='secondary' style={{ marginTop: 4, display: 'block' }}>
              {(project as ProjectWithExtras).description}
            </Text>
          ) : null}
        </div>
        <Button
          icon={<SettingOutlined />}
          onClick={() => navigate(`/projects/${projectId}/settings`)}
        >
          Settings
        </Button>
      </div>

      {/* Sub-navigation */}
      <ProjectSubNav projectId={projectId} activeKey='overview' />

      {/* Pipeline Overview */}
      <Card style={{ marginBottom: 24 }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 24,
            padding: '8px 0',
          }}
        >
          <div style={{ textAlign: 'center' }}>
            <Link to={`/projects/${projectId}/datapacks`}>
              <div
                style={{
                  padding: '8px 24px',
                  background: '#f0f5ff',
                  borderRadius: 8,
                  cursor: 'pointer',
                }}
              >
                <div style={{ fontSize: 20, fontWeight: 600 }}>
                  {datapackTotal}
                </div>
                <div style={{ color: '#666' }}>Datapacks</div>
              </div>
            </Link>
            <div style={{ marginTop: 4 }}>
              {renderStatusDots(datapacksByState)}
            </div>
          </div>

          <RightOutlined style={{ fontSize: 20, color: '#999' }} />

          <div style={{ textAlign: 'center' }}>
            <Link to={`/projects/${projectId}/executions`}>
              <div
                style={{
                  padding: '8px 24px',
                  background: '#f0f5ff',
                  borderRadius: 8,
                  cursor: 'pointer',
                }}
              >
                <div style={{ fontSize: 20, fontWeight: 600 }}>
                  {executionTotal}
                </div>
                <div style={{ color: '#666' }}>Executions</div>
              </div>
            </Link>
            <div style={{ marginTop: 4 }}>
              {renderStatusDots(executionsByState)}
            </div>
          </div>

          <RightOutlined style={{ fontSize: 20, color: '#999' }} />

          <div style={{ textAlign: 'center' }}>
            <Link to={`/projects/${projectId}/evaluations`}>
              <div
                style={{
                  padding: '8px 24px',
                  background: '#f0f5ff',
                  borderRadius: 8,
                  cursor: 'pointer',
                }}
              >
                <div style={{ fontSize: 20, fontWeight: 600 }}>
                  <BarChartOutlined />
                </div>
                <div style={{ color: '#666' }}>Results</div>
              </div>
            </Link>
          </div>
        </div>
      </Card>

      {/* Quick Actions */}
      <Card size='small' style={{ marginBottom: 24 }}>
        <Space>
          <Button
            type='primary'
            icon={<PlusOutlined />}
            onClick={() => navigate(`/projects/${projectId}/inject`)}
          >
            Inject Faults
          </Button>
          <Button
            icon={<PlayCircleOutlined />}
            onClick={() => navigate(`/projects/${projectId}/execute`)}
          >
            Run Algorithm
          </Button>
        </Space>
      </Card>

      {/* Recent Datapacks */}
      <Row gutter={24}>
        <Col span={24} style={{ marginBottom: 24 }}>
          <Card
            title={`Datapacks (${datapackTotal})`}
            extra={
              <Link to={`/projects/${projectId}/datapacks`}>
                View All <RightOutlined />
              </Link>
            }
          >
            <Table
              columns={datapackColumns}
              dataSource={datapacks}
              rowKey='id'
              size='small'
              pagination={false}
              onRow={(record) => ({
                onClick: () => navigate(`/datapacks/${record.id}`),
                style: { cursor: 'pointer' },
              })}
            />
          </Card>
        </Col>

        {/* Recent Executions */}
        <Col span={24}>
          <Card
            title={`Executions (${executionTotal})`}
            extra={
              <Link to={`/projects/${projectId}/executions`}>
                View All <RightOutlined />
              </Link>
            }
          >
            <Table
              columns={executionColumns}
              dataSource={executions}
              rowKey='id'
              size='small'
              pagination={false}
              onRow={(record) => ({
                onClick: () => navigate(`/executions/${record.id}`),
                style: { cursor: 'pointer' },
              })}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default ProjectDetail;
