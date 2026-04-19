import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, Space, Typography } from 'antd';

import type { FaultEntry } from '../types';

import ParamEditor from './ParamEditor';

const { Text, Title } = Typography;

interface FaultStepProps {
  faultSpecs: FaultEntry[];
  onAdd: () => void;
  onRemove: (idx: number) => void;
  onUpdate: (idx: number, patch: Partial<FaultEntry>) => void;
}

const FaultStep: React.FC<FaultStepProps> = ({
  faultSpecs,
  onAdd,
  onRemove,
  onUpdate,
}) => {
  return (
    <div>
      <Title level={5}>Configure Fault Specifications</Title>
      <Text type='secondary' style={{ display: 'block', marginBottom: 16 }}>
        Define one or more fault nodes. Each fault requires an action, mode, and
        duration. You can also add custom parameters.
      </Text>
      {faultSpecs.map((fault, idx) => (
        <Card
          key={idx}
          size='small'
          title={`Fault #${idx + 1}`}
          extra={
            faultSpecs.length > 1 ? (
              <Button
                type='text'
                danger
                icon={<DeleteOutlined />}
                onClick={() => onRemove(idx)}
              />
            ) : null
          }
          style={{ marginBottom: 12 }}
        >
          <Form layout='vertical'>
            <Space wrap style={{ display: 'flex', gap: 12, marginBottom: 8 }}>
              <Form.Item
                label='Action'
                required
                style={{ marginBottom: 0, minWidth: 180 }}
              >
                <Input
                  placeholder='e.g. pod-kill'
                  value={fault.action}
                  onChange={(e) => onUpdate(idx, { action: e.target.value })}
                />
              </Form.Item>
              <Form.Item
                label='Mode'
                required
                style={{ marginBottom: 0, minWidth: 140 }}
              >
                <Input
                  placeholder='e.g. one'
                  value={fault.mode}
                  onChange={(e) => onUpdate(idx, { mode: e.target.value })}
                />
              </Form.Item>
              <Form.Item
                label='Duration'
                required
                style={{ marginBottom: 0, minWidth: 120 }}
              >
                <Input
                  placeholder='e.g. 30s'
                  value={fault.duration}
                  onChange={(e) => onUpdate(idx, { duration: e.target.value })}
                />
              </Form.Item>
            </Space>
            <Form.Item label='Parameters' style={{ marginBottom: 0 }}>
              <ParamEditor
                value={fault.params}
                onChange={(params) => onUpdate(idx, { params })}
              />
            </Form.Item>
          </Form>
        </Card>
      ))}
      <Button type='dashed' icon={<PlusOutlined />} onClick={onAdd}>
        Add Fault
      </Button>
    </div>
  );
};

export default FaultStep;
