import { FunctionOutlined } from '@ant-design/icons';
import type { ContainerResp } from '@rcabench/client';
import {
  Card,
  Descriptions,
  Form,
  Select,
  Space,
  Switch,
  Typography,
} from 'antd';

const { Text } = Typography;
const { Option } = Select;

interface AlgorithmsSectionProps {
  algorithmsData: { items?: ContainerResp[] } | undefined;
  selectedAlgorithm: string;
  selectedVersion: string;
  onAlgorithmChange: (name: string) => void;
  onVersionChange: (version: string) => void;
}

const AlgorithmsSection: React.FC<AlgorithmsSectionProps> = ({
  algorithmsData,
  selectedAlgorithm,
  selectedVersion,
  onAlgorithmChange,
  onVersionChange,
}) => {
  return (
    <>
      <Form.Item
        label='Algorithm'
        name='algorithm_name'
        rules={[{ required: true, message: 'Please select an algorithm' }]}
      >
        <Select
          placeholder='Select algorithm'
          size='large'
          onChange={onAlgorithmChange}
        >
          {algorithmsData?.items?.map((algorithm) => (
            <Option key={algorithm.id} value={algorithm.name}>
              <Space>
                <FunctionOutlined style={{ color: 'var(--color-warning)' }} />
                <div>
                  <div>{algorithm.name}</div>
                  <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                    Algorithm
                  </Text>
                </div>
              </Space>
            </Option>
          ))}
        </Select>
      </Form.Item>

      {selectedAlgorithm && (
        <>
          <Form.Item
            label='Algorithm Version'
            name='algorithm_version'
            rules={[
              {
                required: true,
                message: 'Please select algorithm version',
              },
            ]}
          >
            <Select
              placeholder='Select version'
              size='large'
              onChange={onVersionChange}
              value={selectedVersion}
            >
              <Option key='latest' value='latest'>
                <Space>
                  <Text>latest</Text>
                  <Text type='secondary' style={{ fontSize: '0.75rem' }}>
                    Default version
                  </Text>
                </Space>
              </Option>
            </Select>
          </Form.Item>

          <Card size='small' style={{ marginBottom: 24 }}>
            <Descriptions column={2} size='small'>
              <Descriptions.Item label='Type'>Algorithm</Descriptions.Item>
              <Descriptions.Item label='Public'>
                <Switch
                  checked={
                    algorithmsData?.items?.find(
                      (a) => a.name === selectedAlgorithm
                    )?.is_public
                  }
                  disabled
                  size='small'
                />
              </Descriptions.Item>
              <Descriptions.Item label='Versions'>1</Descriptions.Item>
              <Descriptions.Item label='Created'>
                {new Date(
                  algorithmsData?.items?.find(
                    (a) => a.name === selectedAlgorithm
                  )?.created_at || ''
                ).toLocaleDateString()}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </>
      )}
    </>
  );
};

export default AlgorithmsSection;
