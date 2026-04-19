import { useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { PlayCircleOutlined } from '@ant-design/icons';
import type {
  ContainerResp,
  ContainerVersionResp,
  ExecutionSpec,
  InjectionResp,
  SubmitExecutionReq,
} from '@rcabench/client';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  message,
  Select,
  Skeleton,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { containerApi } from '@/api/containers';
import { projectApi } from '@/api/projects';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';

const { Title, Text } = Typography;

/**
 * Injection states that indicate a usable datapack.
 * state >= 4 means BuildSuccess or above (build_success, detector_failed, detector_success).
 */
const USABLE_DATAPACK_STATES = new Set([
  'build_success',
  'detector_failed',
  'detector_success',
]);

const CreateExecutionForm: React.FC = () => {
  const { id: projectIdParam } = useParams<{ id: string }>();
  const projectId = Number(projectIdParam);
  const navigate = useNavigate();

  // Form state
  const [selectedAlgorithmId, setSelectedAlgorithmId] = useState<number | null>(
    null
  );
  const [algorithmName, setAlgorithmName] = useState('');
  const [algorithmVersion, setAlgorithmVersion] = useState('');
  const [selectedDatapackNames, setSelectedDatapackNames] = useState<string[]>(
    []
  );
  const [isDirty, setIsDirty] = useState(false);
  const [datapackSearch, setDatapackSearch] = useState('');
  useUnsavedChangesGuard(isDirty);

  // Fetch project detail (need project_name for submission)
  const { data: project, isLoading: projectLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => projectApi.getProjectDetail(projectId),
    enabled: !!projectId,
  });

  // Fetch algorithms (ContainerType.Algorithm = 0)
  const { data: algorithmsData } = useQuery({
    queryKey: ['containers', 'algorithm'],
    queryFn: () => containerApi.getContainers({ type: 0, size: 100 }),
  });
  const algorithms = useMemo(
    () => algorithmsData?.items ?? [],
    [algorithmsData]
  );

  // Fetch versions for the selected algorithm
  const { data: versionsData, isLoading: versionsLoading } = useQuery({
    queryKey: ['container-versions', selectedAlgorithmId],
    queryFn: () =>
      containerApi.getVersions(selectedAlgorithmId as number, { size: 100 }),
    enabled: !!selectedAlgorithmId,
  });
  const versions = useMemo(() => versionsData?.items ?? [], [versionsData]);

  // Fetch project datapacks
  const { data: datapacksData, isLoading: datapacksLoading } = useQuery({
    queryKey: ['project-datapacks', projectId],
    queryFn: () => projectApi.listProjectInjections(projectId, { size: 100 }),
    enabled: !!projectId,
  });

  // Filter to only usable datapacks (state >= BuildSuccess)
  const usableDatapacks = useMemo(() => {
    const all = datapacksData?.items ?? [];
    return all.filter((dp: InjectionResp) =>
      dp.state ? USABLE_DATAPACK_STATES.has(dp.state.toLowerCase()) : false
    );
  }, [datapacksData]);

  // Filter datapacks by search text
  const filteredDatapacks = useMemo(() => {
    if (!datapackSearch) return usableDatapacks;
    const lower = datapackSearch.toLowerCase();
    return usableDatapacks.filter(
      (dp: InjectionResp) =>
        dp.name?.toLowerCase().includes(lower) ||
        dp.fault_type?.toLowerCase().includes(lower) ||
        dp.benchmark_name?.toLowerCase().includes(lower)
    );
  }, [usableDatapacks, datapackSearch]);

  // Handle algorithm selection
  const handleAlgorithmChange = (value: number) => {
    const algo = algorithms.find((a: ContainerResp) => a.id === value);
    setSelectedAlgorithmId(value);
    setAlgorithmName(algo?.name ?? '');
    setAlgorithmVersion('');
    setIsDirty(true);
  };

  // Build specs
  const specs = useMemo((): ExecutionSpec[] => {
    if (!algorithmName || !algorithmVersion) return [];
    return selectedDatapackNames.map((dpName) => ({
      algorithm: { name: algorithmName, version: algorithmVersion },
      datapack: dpName,
    }));
  }, [algorithmName, algorithmVersion, selectedDatapackNames]);

  const canSubmit = specs.length > 0 && !!project?.name;

  // Submit mutation
  const submitMutation = useMutation({
    mutationFn: () => {
      const body: SubmitExecutionReq = {
        project_name: (project?.name ?? '') as string,
        specs,
        labels: [],
      };
      return projectApi.executeAlgorithm(projectId, body);
    },
    onSuccess: () => {
      message.success('Execution submitted successfully');
      setIsDirty(false);
      navigate(`/projects/${projectId}/executions`);
    },
    onError: () => {
      message.error('Failed to submit execution');
    },
  });

  const handleSubmit = () => {
    if (!canSubmit) return;
    submitMutation.mutate();
  };

  const handleCancel = () => {
    navigate(`/projects/${projectId}`);
  };

  if (projectLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    );
  }

  return (
    <div style={{ padding: 24, maxWidth: 800 }}>
      <Title level={3}>Create Execution</Title>
      {project?.name && (
        <Text type='secondary' style={{ display: 'block', marginBottom: 24 }}>
          Project: {project.name}
        </Text>
      )}

      <Card title='Select Algorithm' style={{ marginBottom: 16 }}>
        <Form layout='vertical'>
          <Form.Item label='Algorithm' required>
            <Select
              placeholder='Select an algorithm'
              value={selectedAlgorithmId ?? undefined}
              onChange={handleAlgorithmChange}
              options={algorithms.map((a: ContainerResp) => ({
                value: a.id,
                label: a.name,
              }))}
              style={{ maxWidth: 360 }}
            />
          </Form.Item>
          {selectedAlgorithmId && (
            <Form.Item label='Version' required>
              <Select
                placeholder={
                  versionsLoading ? 'Loading versions...' : 'Select a version'
                }
                value={algorithmVersion || undefined}
                onChange={setAlgorithmVersion}
                loading={versionsLoading}
                options={versions.map((v: ContainerVersionResp) => ({
                  value: v.name,
                  label: v.name,
                }))}
                style={{ maxWidth: 360 }}
              />
            </Form.Item>
          )}
        </Form>
      </Card>

      <Card title='Select Datapacks' style={{ marginBottom: 16 }}>
        {datapacksLoading ? (
          <Skeleton active paragraph={{ rows: 3 }} />
        ) : usableDatapacks.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description='No datapacks available. Only datapacks with BuildSuccess or later state can be used.'
          />
        ) : (
          <>
            <Input.Search
              placeholder='Search datapacks by name, fault type, or benchmark...'
              allowClear
              onChange={(e) => setDatapackSearch(e.target.value)}
              style={{ marginBottom: 12, maxWidth: 400 }}
            />
            <Text type='secondary' style={{ display: 'block', marginBottom: 8 }}>
              {usableDatapacks.length} available datapack
              {usableDatapacks.length !== 1 ? 's' : ''}
              {selectedDatapackNames.length > 0 &&
                ` — ${selectedDatapackNames.length} selected`}
            </Text>
            <Table<InjectionResp>
              rowKey='name'
              dataSource={filteredDatapacks}
              size='small'
              rowSelection={{
                selectedRowKeys: selectedDatapackNames,
                onChange: (keys) =>
                  setSelectedDatapackNames(keys as string[]),
              }}
              columns={
                [
                  {
                    title: 'Name',
                    dataIndex: 'name',
                    key: 'name',
                    render: (name: string) => (
                      <Text strong>{name}</Text>
                    ),
                  },
                  {
                    title: 'Fault Type',
                    dataIndex: 'fault_type',
                    key: 'fault_type',
                    filters: [
                      ...new Set(
                        usableDatapacks
                          .map((dp: InjectionResp) => dp.fault_type)
                          .filter(Boolean)
                      ),
                    ].map((ft) => ({ text: ft as string, value: ft as string })),
                    onFilter: (value, record) =>
                      record.fault_type === value,
                  },
                  {
                    title: 'State',
                    dataIndex: 'state',
                    key: 'state',
                    render: (state: string) => (
                      <Tag>{state}</Tag>
                    ),
                    filters: [
                      ...new Set(
                        usableDatapacks
                          .map((dp: InjectionResp) => dp.state)
                          .filter(Boolean)
                      ),
                    ].map((s) => ({ text: s as string, value: s as string })),
                    onFilter: (value, record) =>
                      record.state === value,
                  },
                  {
                    title: 'Benchmark',
                    dataIndex: 'benchmark_name',
                    key: 'benchmark_name',
                    render: (name: string) => name ?? '-',
                  },
                ] as ColumnsType<InjectionResp>
              }
              pagination={false}
              scroll={{ y: 320 }}
            />
          </>
        )}
      </Card>

      <Space>
        <Button
          type='primary'
          icon={<PlayCircleOutlined />}
          size='large'
          disabled={!canSubmit}
          loading={submitMutation.isPending}
          onClick={handleSubmit}
        >
          Run {specs.length} Execution{specs.length !== 1 ? 's' : ''}
        </Button>
        <Button size='large' onClick={handleCancel}>
          Cancel
        </Button>
      </Space>
    </div>
  );
};

export default CreateExecutionForm;
