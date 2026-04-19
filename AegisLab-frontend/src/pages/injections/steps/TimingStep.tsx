import { InfoCircleOutlined } from '@ant-design/icons';
import { Form, InputNumber, Tooltip, Typography } from 'antd';

const { Title } = Typography;

interface TimingStepProps {
  injectionInterval: number;
  onIntervalChange: (v: number) => void;
  preDuration: number;
  onPreDurationChange: (v: number) => void;
}

const TimingStep: React.FC<TimingStepProps> = ({
  injectionInterval,
  onIntervalChange,
  preDuration,
  onPreDurationChange,
}) => {
  return (
    <div style={{ maxWidth: 480 }}>
      <Title level={5}>Timing Configuration</Title>
      <Form layout='vertical'>
        <Form.Item
          label={
            <span>
              Interval (minutes)&nbsp;
              <Tooltip title='Time between consecutive fault injections'>
                <InfoCircleOutlined />
              </Tooltip>
            </span>
          }
          required
        >
          <InputNumber
            min={1}
            value={injectionInterval}
            onChange={(v) => onIntervalChange(v ?? 5)}
            style={{ width: '100%' }}
            addonAfter='min'
          />
        </Form.Item>
        <Form.Item
          label={
            <span>
              Pre-duration (minutes)&nbsp;
              <Tooltip title='How long to collect normal (non-fault) data before beginning injections'>
                <InfoCircleOutlined />
              </Tooltip>
            </span>
          }
          required
        >
          <InputNumber
            min={0}
            value={preDuration}
            onChange={(v) => onPreDurationChange(v ?? 3)}
            style={{ width: '100%' }}
            addonAfter='min'
          />
        </Form.Item>
      </Form>
    </div>
  );
};

export default TimingStep;
