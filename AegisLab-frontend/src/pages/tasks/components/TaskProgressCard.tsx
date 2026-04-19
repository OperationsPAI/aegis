import { SyncOutlined } from '@ant-design/icons';
import { TaskState } from '@rcabench/client';
import { Card, Progress, Space, Typography } from 'antd';

import { getStateColor, getStateIcon } from '../utils/taskHelpers';

const { Text } = Typography;

interface TaskProgressCardProps {
  state: TaskState;
  isRunning: boolean;
  isRescheduled: boolean;
}

const TaskProgressCard: React.FC<TaskProgressCardProps> = ({
  state,
  isRunning,
  isRescheduled,
}) => {
  const isIndeterminate = isRunning || isRescheduled;
  const progress = isIndeterminate
    ? 100
    : state === TaskState.Completed
      ? 100
      : 0;

  return (
    <Card style={{ marginBottom: 24 }}>
      <div style={{ marginBottom: 16 }}>
        <Text strong>Task Progress</Text>
      </div>
      <Progress
        percent={progress}
        status={
          state === TaskState.Error
            ? 'exception'
            : state === TaskState.Completed
              ? 'success'
              : 'active'
        }
        strokeColor={getStateColor(state)}
        showInfo={!isIndeterminate}
        format={(percent) => (
          <Space>
            {getStateIcon(state)}
            <Text>{percent}%</Text>
          </Space>
        )}
      />
      {isIndeterminate && (
        <Text type='secondary' style={{ marginTop: 8, display: 'block' }}>
          <SyncOutlined spin style={{ marginRight: 4 }} />
          {isRunning ? 'Task is running...' : 'Task is rescheduled, waiting...'}
        </Text>
      )}
    </Card>
  );
};

export default TaskProgressCard;
