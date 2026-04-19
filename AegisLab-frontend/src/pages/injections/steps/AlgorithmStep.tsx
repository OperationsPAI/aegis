import { useState } from 'react';

import type { ContainerVersionResp } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Alert, Form, message, Select, Tag, Typography } from 'antd';

import { containerApi } from '@/api/containers';

import type { AlgorithmSelection } from '../types';

const { Text, Title } = Typography;

interface AlgorithmStepProps {
  value: AlgorithmSelection[];
  onChange: (v: AlgorithmSelection[]) => void;
}

const AlgorithmStep: React.FC<AlgorithmStepProps> = ({ value, onChange }) => {
  const { data: algorithmsData, isLoading } = useQuery({
    queryKey: ['containers', 0],
    queryFn: () => containerApi.getContainers({ type: 0, size: 100 }),
  });

  const algorithmContainers = algorithmsData?.items ?? [];

  // Track which container is being version-selected
  const [pendingContainerId, setPendingContainerId] = useState<number | null>(
    null
  );

  const { data: versionsData, isLoading: versionsLoading } = useQuery({
    queryKey: ['containerVersions', pendingContainerId],
    queryFn: () => containerApi.getVersions(pendingContainerId as number),
    enabled: !!pendingContainerId,
  });

  const pendingVersions: ContainerVersionResp[] = versionsData?.items ?? [];

  const handleAddAlgorithm = (containerId: number) => {
    setPendingContainerId(containerId);
  };

  const handleSelectVersion = (versionName: string) => {
    if (!pendingContainerId) return;
    const container = algorithmContainers.find(
      (c) => c.id === pendingContainerId
    );
    if (!container) return;

    // Avoid duplicates
    const exists = value.some(
      (a) => a.name === container.name && a.version === versionName
    );
    if (exists) {
      message.info('This algorithm version is already selected.');
      setPendingContainerId(null);
      return;
    }

    onChange([
      ...value,
      {
        containerId: container.id as number,
        name: container.name as string,
        version: versionName,
      },
    ]);
    setPendingContainerId(null);
  };

  const handleRemove = (idx: number) => {
    onChange(value.filter((_, i) => i !== idx));
  };

  return (
    <div style={{ maxWidth: 560 }}>
      <Title level={5}>Select Algorithms (Optional)</Title>
      <Text type='secondary' style={{ display: 'block', marginBottom: 16 }}>
        Optionally choose algorithms to auto-run after data collection
        completes.
      </Text>

      {value.length > 0 && (
        <div style={{ marginBottom: 16 }}>
          {value.map((a, idx) => (
            <Tag
              key={`${a.name}-${a.version}`}
              closable
              onClose={() => handleRemove(idx)}
              style={{ marginBottom: 4 }}
            >
              {a.name}:{a.version}
            </Tag>
          ))}
        </div>
      )}

      <Form layout='vertical'>
        <Form.Item label='Algorithm'>
          <Select
            placeholder='Choose an algorithm to add...'
            loading={isLoading}
            value={pendingContainerId ?? undefined}
            onChange={handleAddAlgorithm}
            showSearch
            optionFilterProp='label'
            options={algorithmContainers.map((c) => ({
              value: c.id,
              label: c.name,
            }))}
          />
        </Form.Item>

        {pendingContainerId && (
          <Form.Item label='Version'>
            <Select
              placeholder='Choose a version...'
              loading={versionsLoading}
              onChange={handleSelectVersion}
              options={pendingVersions.map((v) => ({
                value: v.name,
                label: v.name,
              }))}
            />
          </Form.Item>
        )}
      </Form>

      {value.length === 0 && !pendingContainerId && (
        <Alert
          type='info'
          showIcon
          message='No algorithms selected. You can skip this step.'
        />
      )}
    </div>
  );
};

export default AlgorithmStep;
