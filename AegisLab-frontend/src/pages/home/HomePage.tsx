import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import {
  ArrowRightOutlined,
  FolderOutlined,
  OrderedListOutlined,
  PlusOutlined,
  RocketOutlined,
} from '@ant-design/icons';
import type { ProjectResp } from '@rcabench/client';
import {
  Button,
  Card,
  Col,
  Empty,
  List,
  Row,
  Skeleton,
  Space,
  Statistic,
  Steps,
  Typography,
} from 'antd';
import dayjs from 'dayjs';

import { useProjects } from '@/hooks/useProjects';
import CreateProjectModal from '@/pages/projects/CreateProjectModal';
import { useAuthStore } from '@/store/auth';

import './HomePage.css';

const { Title, Text, Paragraph } = Typography;

/**
 * Home Page
 * Personal dashboard showing recent projects, quick stats, and getting started guide
 */
const HomePage: React.FC = () => {
  const navigate = useNavigate();
  const { user } = useAuthStore();
  const [createModalOpen, setCreateModalOpen] = useState(false);

  // Fetch recent projects
  const { data: projectsData, isLoading } = useProjects({
    page: 1,
    size: 10,
    queryKey: ['projects', 'recent'],
  });

  const recentProjects = projectsData?.items || [];

  return (
    <div className='home-page'>
      {/* Welcome Section */}
      <div className='welcome-section'>
        <Title level={4}>Welcome back, {user?.username || 'User'}</Title>
        <Paragraph type='secondary'>
          Manage your RCA benchmarking projects and experiments
        </Paragraph>
      </div>

      {/* Quick Actions */}
      <Card className='quick-actions-card' style={{ marginBottom: 24 }}>
        <Space size='large' wrap>
          <Button
            type='primary'
            size='large'
            icon={<PlusOutlined />}
            onClick={() => setCreateModalOpen(true)}
          >
            Create Project
          </Button>
          <Button
            size='large'
            icon={<OrderedListOutlined />}
            onClick={() => navigate('/tasks')}
          >
            View Tasks
          </Button>
        </Space>
      </Card>

      {/* Quick Stats */}
      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col xs={24} sm={12}>
          <Card>
            <Statistic
              title='Total Projects'
              value={projectsData?.pagination?.total ?? '-'}
              prefix={<FolderOutlined />}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={[24, 24]}>
        {/* Recent Projects */}
        <Col xs={24} lg={14}>
          <Card
            title={
              <Space>
                <FolderOutlined />
                <span>Recent Projects</span>
              </Space>
            }
            extra={
              <Button type='link' onClick={() => navigate('/projects')}>
                View all <ArrowRightOutlined />
              </Button>
            }
          >
            {isLoading ? (
              <Skeleton active paragraph={{ rows: 3 }} />
            ) : recentProjects.length > 0 ? (
              <List
                itemLayout='horizontal'
                dataSource={recentProjects}
                renderItem={(project: ProjectResp) => (
                  <List.Item
                    className='project-list-item'
                    onClick={() => navigate(`/projects/${project.id}`)}
                    style={{ cursor: 'pointer' }}
                    actions={[
                      <Button
                        type='link'
                        key='open'
                        icon={<ArrowRightOutlined />}
                      >
                        Open
                      </Button>,
                    ]}
                  >
                    <List.Item.Meta
                      avatar={
                        <FolderOutlined
                          style={{
                            fontSize: 24,
                            color: 'var(--color-primary-500)',
                          }}
                        />
                      }
                      title={<Text strong>{project.name}</Text>}
                      description={
                        <Space size='small'>
                          <Text type='secondary'>
                            Created {dayjs(project.created_at).fromNow()}
                          </Text>
                        </Space>
                      }
                    />
                  </List.Item>
                )}
              />
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description='No projects yet'
              >
                <Button
                  type='primary'
                  icon={<PlusOutlined />}
                  onClick={() => setCreateModalOpen(true)}
                >
                  Create Your First Project
                </Button>
              </Empty>
            )}
          </Card>
        </Col>

        {/* Getting Started — always visible */}
        <Col xs={24} lg={10}>
          <Card
            title={
              <Space>
                <RocketOutlined />
                <span>Getting Started</span>
              </Space>
            }
          >
            <Steps
              direction='vertical'
              size='small'
              current={-1}
              items={[
                {
                  title: 'Create a Project',
                  description: 'Organize your RCA experiments',
                },
                {
                  title: 'Inject Faults',
                  description: 'Configure and run fault injection pipelines',
                },
                {
                  title: 'Run Algorithms',
                  description: 'Execute RCA algorithms on collected data',
                },
                {
                  title: 'Evaluate Results',
                  description: 'Compare algorithm performance',
                },
              ]}
            />
          </Card>
        </Col>
      </Row>

      <CreateProjectModal
        open={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        onSuccess={(project) => {
          setCreateModalOpen(false);
          navigate(`/projects/${project.id}`);
        }}
      />
    </div>
  );
};

export default HomePage;
