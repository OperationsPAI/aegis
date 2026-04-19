import { DatabaseOutlined } from '@ant-design/icons';
import { Form, Select, Space, Typography } from 'antd';

const { Text } = Typography;
const { Option } = Select;

interface EvaluationTypeSelectProps {
  evaluationType: 'datapack' | 'dataset';
  onChange: (type: 'datapack' | 'dataset') => void;
}

const EvaluationTypeSelect: React.FC<EvaluationTypeSelectProps> = ({
  evaluationType,
  onChange,
}) => {
  return (
    <Form.Item
      label='Evaluation Type'
      name='evaluation_type'
      rules={[{ required: true, message: 'Please select evaluation type' }]}
    >
      <Select
        placeholder='Select evaluation type'
        size='large'
        onChange={onChange}
        value={evaluationType}
      >
        <Option value='datapack'>
          <Space>
            <DatabaseOutlined style={{ color: 'var(--color-primary-500)' }} />
            <div>
              <div>Datapack Evaluation</div>
              <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                Evaluate algorithm performance on collected datapacks
              </Text>
            </div>
          </Space>
        </Option>
        <Option value='dataset'>
          <Space>
            <DatabaseOutlined style={{ color: 'var(--color-success)' }} />
            <div>
              <div>Dataset Evaluation</div>
              <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                Evaluate algorithm performance on standard datasets
              </Text>
            </div>
          </Space>
        </Option>
      </Select>
    </Form.Item>
  );
};

export default EvaluationTypeSelect;
