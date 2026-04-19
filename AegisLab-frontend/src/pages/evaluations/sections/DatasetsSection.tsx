import { CheckCircleOutlined, DatabaseOutlined } from '@ant-design/icons';
import type { DatasetResp, ExecutionResp } from '@rcabench/client';
import { Form, Select, Space, Typography } from 'antd';

const { Text } = Typography;
const { Option } = Select;

interface DatasetsSectionProps {
  evaluationType: 'datapack' | 'dataset';
  executionsData: { items?: ExecutionResp[] } | undefined;
  datasetsData: { items?: DatasetResp[] } | undefined;
  onDatapackChange: (id: string) => void;
  onDatasetChange: (id: string) => void;
}

const DatasetsSection: React.FC<DatasetsSectionProps> = ({
  evaluationType,
  executionsData,
  datasetsData,
  onDatapackChange,
  onDatasetChange,
}) => {
  return (
    <>
      {evaluationType === 'datapack' && executionsData?.items && (
        <Form.Item
          label='Datapack'
          name='datapack_id'
          rules={[{ required: true, message: 'Please select a datapack' }]}
        >
          <Select
            placeholder='Select datapack'
            size='large'
            onChange={onDatapackChange}
          >
            {executionsData?.items?.map((execution: ExecutionResp) => (
              <Option
                key={execution.id}
                value={String(execution.datapack_id) || ''}
              >
                <Space>
                  <DatabaseOutlined
                    style={{ color: 'var(--color-primary-500)' }}
                  />
                  <div>
                    <div>
                      Datapack{' '}
                      {execution.datapack_name || execution.datapack_id}
                    </div>
                    <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                      From execution #{execution.id} -{' '}
                      {execution.algorithm_name}
                    </Text>
                  </div>
                </Space>
              </Option>
            ))}
          </Select>
        </Form.Item>
      )}

      {evaluationType === 'dataset' && datasetsData?.items && (
        <Form.Item
          label='Dataset'
          name='dataset_id'
          rules={[{ required: true, message: 'Please select a dataset' }]}
        >
          <Select
            placeholder='Select dataset'
            size='large'
            onChange={onDatasetChange}
          >
            {datasetsData?.items?.map((dataset: DatasetResp) => (
              <Option key={dataset.id} value={String(dataset.id)}>
                <Space>
                  <DatabaseOutlined style={{ color: 'var(--color-success)' }} />
                  <div>
                    <div>{dataset.name}</div>
                    <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                      {dataset.type}
                    </Text>
                  </div>
                </Space>
              </Option>
            ))}
          </Select>
        </Form.Item>
      )}

      <Form.Item
        label='Groundtruth Dataset (Optional)'
        name='groundtruth_dataset_id'
      >
        <Select
          placeholder='Select groundtruth dataset (optional)'
          size='large'
          allowClear
        >
          {datasetsData?.items?.map((dataset) => (
            <Option key={dataset.id} value={String(dataset.id)}>
              <Space>
                <CheckCircleOutlined
                  style={{ color: 'var(--color-success)' }}
                />
                <div>
                  <div>{dataset.name}</div>
                  <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                    {dataset.type}
                  </Text>
                </div>
              </Space>
            </Option>
          ))}
        </Select>
      </Form.Item>
    </>
  );
};

export default DatasetsSection;
