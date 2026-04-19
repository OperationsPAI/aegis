import { SearchOutlined } from '@ant-design/icons';
import { ListTasksTaskType, TaskState } from '@rcabench/client';
import { Card, Col, Input, Row, Select } from 'antd';

const { Search } = Input;
const { Option } = Select;

interface TaskFiltersProps {
  typeFilter: ListTasksTaskType | undefined;
  stateFilter: TaskState | undefined;
  onSearch: (value: string) => void;
  onTypeFilter: (type: ListTasksTaskType | undefined) => void;
  onStateFilter: (state: TaskState | undefined) => void;
}

const TaskFilters: React.FC<TaskFiltersProps> = ({
  typeFilter,
  stateFilter,
  onSearch,
  onTypeFilter,
  onStateFilter,
}) => {
  return (
    <Card style={{ marginBottom: 16 }}>
      <Row gutter={[16, 16]} align='middle'>
        <Col xs={24} sm={12} md={6}>
          <Search
            placeholder='Search tasks by ID or type...'
            allowClear
            enterButton={<SearchOutlined />}
            onSearch={onSearch}
            style={{ width: '100%' }}
          />
        </Col>
        <Col xs={24} sm={12} md={4}>
          <Select
            placeholder='Filter by type'
            allowClear
            style={{ width: '100%' }}
            onChange={onTypeFilter}
            value={typeFilter}
          >
            <Option value={ListTasksTaskType.NUMBER_0}>Build Container</Option>
            <Option value={ListTasksTaskType.NUMBER_1}>Restart Pedestal</Option>
            <Option value={ListTasksTaskType.NUMBER_2}>Fault Injection</Option>
            <Option value={ListTasksTaskType.NUMBER_3}>Run Algorithm</Option>
            <Option value={ListTasksTaskType.NUMBER_4}>Build Datapack</Option>
            <Option value={ListTasksTaskType.NUMBER_5}>Collect Result</Option>
            <Option value={ListTasksTaskType.NUMBER_6}>Cron Job</Option>
          </Select>
        </Col>
        <Col xs={24} sm={12} md={4}>
          <Select
            placeholder='Filter by status'
            allowClear
            style={{ width: '100%' }}
            onChange={onStateFilter}
            value={stateFilter}
          >
            <Option value={TaskState.Pending}>Pending</Option>
            <Option value={TaskState.Rescheduled}>Rescheduled</Option>
            <Option value={TaskState.Running}>Running</Option>
            <Option value={TaskState.Completed}>Completed</Option>
            <Option value={TaskState.Error}>Error</Option>
            <Option value={TaskState.Cancelled}>Cancelled</Option>
          </Select>
        </Col>
      </Row>
    </Card>
  );
};

export default TaskFilters;
