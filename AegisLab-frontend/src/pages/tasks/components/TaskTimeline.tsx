import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import { type TaskDetailResp, TaskState } from '@rcabench/client';
import { Card, Timeline, Typography } from 'antd';
import dayjs from 'dayjs';

import { getTaskState } from '../utils/taskHelpers';

const { Text } = Typography;

const TaskTimeline: React.FC<{ task: TaskDetailResp }> = ({ task }) => {
  const state = getTaskState(task.state);
  const fmt = (d?: string) =>
    d ? dayjs(d).format('MMM D, YYYY HH:mm:ss') : 'N/A';

  return (
    <Card title='Task Execution Timeline'>
      <Timeline>
        <Timeline.Item color='blue' dot={<ClockCircleOutlined />}>
          <Text strong>Task Created</Text>
          <br />
          <Text type='secondary'>{fmt(task.created_at)}</Text>
        </Timeline.Item>

        {state === TaskState.Running && (
          <Timeline.Item color='green' dot={<PlayCircleOutlined />}>
            <Text strong>Task Started</Text>
            <br />
            <Text type='secondary'>{fmt(task.updated_at)}</Text>
          </Timeline.Item>
        )}

        {state === TaskState.Running && (
          <Timeline.Item color='blue' dot={<SyncOutlined spin />}>
            <Text strong>Task Running</Text>
            <br />
            <Text type='secondary'>In progress...</Text>
          </Timeline.Item>
        )}

        {state === TaskState.Completed && (
          <Timeline.Item color='green' dot={<CheckCircleOutlined />}>
            <Text strong>Task Completed</Text>
            <br />
            <Text type='secondary'>{fmt(task.updated_at)}</Text>
          </Timeline.Item>
        )}

        {state === TaskState.Error && (
          <Timeline.Item color='red' dot={<CloseCircleOutlined />}>
            <Text strong>Task Failed</Text>
            <br />
            <Text type='secondary'>{fmt(task.updated_at)}</Text>
          </Timeline.Item>
        )}

        {state === TaskState.Cancelled && (
          <Timeline.Item color='orange' dot={<PauseCircleOutlined />}>
            <Text strong>Task Cancelled</Text>
            <br />
            <Text type='secondary'>{fmt(task.updated_at)}</Text>
          </Timeline.Item>
        )}
      </Timeline>
    </Card>
  );
};

export default TaskTimeline;
