import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  DashboardOutlined,
  PauseCircleOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import { Card, Col, Row, Statistic } from 'antd';

interface TaskStatsProps {
  total: number;
  pending: number;
  running: number;
  completed: number;
  error: number;
  cancelled: number;
}

const TaskStats: React.FC<TaskStatsProps> = ({
  total,
  pending,
  running,
  completed,
  error,
  cancelled,
}) => {
  return (
    <Row
      gutter={[
        { xs: 8, sm: 16, lg: 24 },
        { xs: 8, sm: 16, lg: 24 },
      ]}
      className='stats-row'
    >
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Total Tasks'
            value={total}
            prefix={<DashboardOutlined />}
            valueStyle={{ color: 'var(--color-primary-500)' }}
          />
        </Card>
      </Col>
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Pending'
            value={pending}
            prefix={<ClockCircleOutlined />}
            valueStyle={{ color: 'var(--color-secondary-500)' }}
          />
        </Card>
      </Col>
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Running'
            value={running}
            prefix={<SyncOutlined />}
            valueStyle={{ color: 'var(--color-primary-500)' }}
          />
        </Card>
      </Col>
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Completed'
            value={completed}
            prefix={<CheckCircleOutlined />}
            valueStyle={{ color: 'var(--color-success)' }}
          />
        </Card>
      </Col>
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Error'
            value={error}
            prefix={<CloseCircleOutlined />}
            valueStyle={{ color: 'var(--color-error)' }}
          />
        </Card>
      </Col>
      <Col xs={12} sm={12} lg={4}>
        <Card>
          <Statistic
            title='Cancelled'
            value={cancelled}
            prefix={<PauseCircleOutlined />}
            valueStyle={{ color: 'var(--color-secondary-500)' }}
          />
        </Card>
      </Col>
    </Row>
  );
};

export default TaskStats;
