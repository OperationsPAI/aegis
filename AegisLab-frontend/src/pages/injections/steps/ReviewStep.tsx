import type { ContainerResp } from '@rcabench/client';
import { Card, Descriptions, Divider, Tag, Typography } from 'antd';

import type { AlgorithmSelection, FaultEntry } from '../types';

const { Text, Title } = Typography;

interface ReviewStepProps {
  projectName: string;
  pedestalContainer: ContainerResp | null;
  pedestalVersion: string;
  benchmarkContainer: ContainerResp | null;
  benchmarkVersion: string;
  injectionInterval: number;
  preDuration: number;
  faultSpecs: FaultEntry[];
  selectedAlgorithms: AlgorithmSelection[];
}

const ReviewStep: React.FC<ReviewStepProps> = ({
  projectName,
  pedestalContainer,
  pedestalVersion,
  benchmarkContainer,
  benchmarkVersion,
  injectionInterval,
  preDuration,
  faultSpecs,
  selectedAlgorithms,
}) => {
  return (
    <div>
      <Title level={5}>Review Injection Configuration</Title>
      <Descriptions bordered column={1} size='small'>
        <Descriptions.Item label='Project'>
          {projectName || '-'}
        </Descriptions.Item>
        <Descriptions.Item label='Pedestal'>
          {pedestalContainer?.name ?? '-'}{' '}
          {pedestalVersion && <Tag>{pedestalVersion}</Tag>}
        </Descriptions.Item>
        <Descriptions.Item label='Benchmark'>
          {benchmarkContainer?.name ?? '-'}{' '}
          {benchmarkVersion && <Tag>{benchmarkVersion}</Tag>}
        </Descriptions.Item>
        <Descriptions.Item label='Interval'>
          {injectionInterval} min
        </Descriptions.Item>
        <Descriptions.Item label='Pre-duration'>
          {preDuration} min
        </Descriptions.Item>
        <Descriptions.Item label='Faults'>
          {faultSpecs.length} fault(s) in 1 group
        </Descriptions.Item>
        {selectedAlgorithms.length > 0 && (
          <Descriptions.Item label='Algorithms'>
            {selectedAlgorithms.map((a) => (
              <Tag key={`${a.name}-${a.version}`}>
                {a.name}:{a.version}
              </Tag>
            ))}
          </Descriptions.Item>
        )}
      </Descriptions>

      <Divider orientation='left' plain>
        Fault Details
      </Divider>
      {faultSpecs.map((f, idx) => (
        <Card key={idx} size='small' style={{ marginBottom: 8 }}>
          <Text strong>Fault #{idx + 1}</Text>
          <br />
          <Text>
            Action: <Tag>{f.action}</Tag> Mode: <Tag>{f.mode}</Tag> Duration:{' '}
            <Tag>{f.duration}</Tag>
          </Text>
          {Object.keys(f.params).length > 0 && (
            <div style={{ marginTop: 4 }}>
              Params:{' '}
              {Object.entries(f.params).map(([k, v]) => (
                <Tag key={k}>
                  {k}={v}
                </Tag>
              ))}
            </div>
          )}
        </Card>
      ))}
    </div>
  );
};

export default ReviewStep;
