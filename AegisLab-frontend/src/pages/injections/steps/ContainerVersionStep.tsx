import type { ContainerResp, ContainerVersionResp } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Form, Select, Typography } from 'antd';

import { containerApi } from '@/api/containers';

const { Title } = Typography;

interface ContainerVersionStepProps {
  containerType: 0 | 1 | 2;
  label: string;
  selectedContainer: ContainerResp | null;
  selectedVersion: string;
  onContainerChange: (c: ContainerResp | null) => void;
  onVersionChange: (v: string) => void;
}

const ContainerVersionStep: React.FC<ContainerVersionStepProps> = ({
  containerType,
  label,
  selectedContainer,
  selectedVersion,
  onContainerChange,
  onVersionChange,
}) => {
  const { data: containersData, isLoading: containersLoading } = useQuery({
    queryKey: ['containers', containerType],
    queryFn: () =>
      containerApi.getContainers({ type: containerType, size: 100 }),
  });

  const containers = containersData?.items ?? [];

  const containerId = selectedContainer?.id;
  const { data: versionsData, isLoading: versionsLoading } = useQuery({
    queryKey: ['containerVersions', containerId],
    queryFn: () => containerApi.getVersions(containerId as number),
    enabled: !!containerId,
  });

  const versions: ContainerVersionResp[] = versionsData?.items ?? [];

  const handleContainerSelect = (id: number) => {
    const found = containers.find((c) => c.id === id) ?? null;
    onContainerChange(found);
    onVersionChange('');
  };

  return (
    <div style={{ maxWidth: 480 }}>
      <Title level={5}>Select {label}</Title>
      <Form layout='vertical'>
        <Form.Item label={`${label} Container`} required>
          <Select
            placeholder={`Choose a ${label.toLowerCase()}...`}
            loading={containersLoading}
            value={selectedContainer?.id ?? undefined}
            onChange={handleContainerSelect}
            showSearch
            optionFilterProp='label'
            options={containers.map((c) => ({
              value: c.id,
              label: c.name,
            }))}
          />
        </Form.Item>

        {selectedContainer && (
          <Form.Item label='Version' required>
            <Select
              placeholder='Choose a version...'
              loading={versionsLoading}
              value={selectedVersion || undefined}
              onChange={(v: string) => onVersionChange(v)}
              options={versions.map((v) => ({
                value: v.name,
                label: v.name,
              }))}
            />
          </Form.Item>
        )}
      </Form>
    </div>
  );
};

export default ContainerVersionStep;
