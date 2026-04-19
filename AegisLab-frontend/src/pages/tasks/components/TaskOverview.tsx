import { CopyOutlined } from '@ant-design/icons';
import type { TaskDetailResp } from '@rcabench/client';
import {
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  message,
  Row,
  Space,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import duration from 'dayjs/plugin/duration';

import StatusBadge from '@/components/ui/StatusBadge';

import {
  getStateName,
  getStatusBadgeKey,
  getTaskState,
  getTaskType,
  getTaskTypeColor,
  getTaskTypeIcon,
} from '../utils/taskHelpers';

dayjs.extend(duration);

const { Title, Text } = Typography;

const TaskOverview: React.FC<{ task: TaskDetailResp; taskId: string }> = ({
  task,
  taskId,
}) => {
  const state = getTaskState(task.state);
  const type = getTaskType(task.type);

  return (
    <>
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={16}>
          <Card title='Task Information'>
            <Descriptions column={2} bordered>
              <Descriptions.Item label='Task ID'>
                <Space>
                  <Text code>{taskId}</Text>
                  <Button
                    type='text'
                    size='small'
                    icon={<CopyOutlined />}
                    onClick={() => {
                      navigator.clipboard.writeText(taskId);
                      message.success('Task ID copied to clipboard');
                    }}
                  />
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label='Type'>
                <Tag
                  color={getTaskTypeColor(type)}
                  style={{ fontWeight: 500, fontSize: '1rem' }}
                >
                  <Space>
                    {getTaskTypeIcon(type)}
                    {task.type || 'Unknown'}
                  </Space>
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label='Status'>
                <StatusBadge
                  status={getStatusBadgeKey(state)}
                  text={getStateName(state)}
                />
              </Descriptions.Item>
              <Descriptions.Item label='Retry Count'>
                <Text code>N/A</Text>
              </Descriptions.Item>
              <Descriptions.Item label='Immediate'>
                <Text>{task.immediate ? 'Yes' : 'No'}</Text>
              </Descriptions.Item>
              <Descriptions.Item label='Trace ID'>
                <Text code>{task.trace_id || 'N/A'}</Text>
              </Descriptions.Item>
              <Descriptions.Item label='Group ID'>
                <Text code>{task.group_id || 'N/A'}</Text>
              </Descriptions.Item>
              <Descriptions.Item label='Project ID'>
                <Text>{task.project_id || 'N/A'}</Text>
              </Descriptions.Item>
              <Descriptions.Item label='Status Code'>
                <Text code>{task.status || 'N/A'}</Text>
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card title='Timing Information'>
            <Space direction='vertical' style={{ width: '100%' }}>
              <div>
                <Text type='secondary'>Created</Text>
                <br />
                <Text strong>
                  {task.created_at
                    ? dayjs(task.created_at).format('MMM D, YYYY HH:mm:ss')
                    : 'N/A'}
                </Text>
              </div>
              <Divider />
              <div>
                <Text type='secondary'>Duration</Text>
                <br />
                <Title
                  level={5}
                  style={{
                    margin: 0,
                    color: 'var(--color-primary-500)',
                  }}
                >
                  {task.created_at && task.updated_at
                    ? (() => {
                        const dur = dayjs.duration(
                          dayjs(task.updated_at).diff(dayjs(task.created_at))
                        );
                        const hours = Math.floor(dur.asHours());
                        const mins = dur.minutes();
                        const secs = dur.seconds();
                        if (hours > 0) return `${hours}h ${mins}m ${secs}s`;
                        if (mins > 0) return `${mins}m ${secs}s`;
                        return `${secs}s`;
                      })()
                    : 'N/A'}
                </Title>
              </div>
            </Space>
          </Card>
        </Col>
      </Row>

      {task.payload !== undefined && task.payload !== null && (
        <Card title='Payload' style={{ marginTop: 16 }}>
          <pre
            style={{
              margin: 0,
              fontSize: '0.875rem',
              whiteSpace: 'pre-wrap',
            }}
          >
            {JSON.stringify(task.payload, null, 2)}
          </pre>
        </Card>
      )}
    </>
  );
};

export default TaskOverview;
