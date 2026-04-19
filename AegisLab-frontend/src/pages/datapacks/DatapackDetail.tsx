import { useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import {
  CopyOutlined,
  DownloadOutlined,
  FolderOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Card,
  Descriptions,
  message,
  Modal,
  Result,
  Skeleton,
  Space,
  Tree,
  Typography,
} from 'antd';
import type { DataNode } from 'antd/es/tree';
import dayjs from 'dayjs';

import { injectionApi } from '@/api/injections';
import StateStepper from '@/components/pipeline/StateStepper';
import TaskLogViewer from '@/components/pipeline/TaskLogViewer';

const { Title } = Typography;

/**
 * Standalone Datapack Detail page.
 * Route: /datapacks/:id
 */

/* ------------------------------------------------------------------
 * Helpers
 * ----------------------------------------------------------------*/

interface FileNode {
  name: string;
  path?: string;
  children?: FileNode[];
}

/** Recursively convert the file list from the API into Ant Design Tree DataNode[] */
const toTreeData = (nodes: FileNode[]): DataNode[] =>
  nodes.map((n) => ({
    key: n.path || n.name,
    title: n.name,
    icon: n.children ? <FolderOutlined /> : undefined,
    isLeaf: !n.children,
    children: n.children ? toTreeData(n.children) : undefined,
  }));

/* ------------------------------------------------------------------
 * Component
 * ----------------------------------------------------------------*/

const DatapackDetail: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const numericId = Number(id);

  // Fetch datapack (injection) detail
  const {
    data: datapack,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ['injection', numericId],
    queryFn: () => injectionApi.getInjection(numericId),
    enabled: !!id && !Number.isNaN(numericId),
    staleTime: 0,
    refetchOnMount: 'always',
  });

  // Fetch file list
  const { data: files } = useQuery({
    queryKey: ['injection-files', numericId],
    queryFn: () => injectionApi.listDatapackFiles(numericId),
    enabled: !!id && !Number.isNaN(numericId),
  });

  const treeData: DataNode[] = useMemo(() => {
    if (!files || !Array.isArray(files)) return [];
    return toTreeData(files as FileNode[]);
  }, [files]);

  /* ---- Action handlers ---- */

  const handleDownload = async () => {
    try {
      const blob = await injectionApi.downloadInjection(numericId);
      const url = window.URL.createObjectURL(new Blob([blob]));
      const link = document.createElement('a');
      link.href = url;
      link.setAttribute('download', `datapack-${id}.tar.gz`);
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      message.success('Download started');
    } catch {
      message.error('Download failed');
    }
  };

  const handleClone = () => {
    Modal.confirm({
      title: 'Clone Datapack',
      content: `Clone datapack "${datapack?.name || id}"?`,
      okText: 'Yes, clone it',
      cancelText: 'Cancel',
      onOk: async () => {
        try {
          await injectionApi.cloneInjection(numericId);
          message.success('Datapack cloned successfully');
        } catch {
          message.error('Failed to clone datapack');
        }
      },
    });
  };

  /* ---- Render states ---- */

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }

  if (isError || !datapack) {
    return (
      <Result
        status='404'
        title='Datapack not found'
        subTitle={`Could not load datapack with ID ${id}.`}
        extra={
          <Button type='primary' onClick={() => navigate(-1)}>
            Go Back
          </Button>
        }
      />
    );
  }

  return (
    <div style={{ padding: 24, maxWidth: 1000, margin: '0 auto' }}>
      {/* ---- Header ---- */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          marginBottom: 24,
        }}
      >
        <div style={{ flex: 1 }}>
          <Title level={3} style={{ marginBottom: 16 }}>
            {datapack.name}
          </Title>
          <div style={{ maxWidth: 480 }}>
            <StateStepper state={datapack.state ?? '0'} />
          </div>
        </div>

        <Space>
          <Button icon={<DownloadOutlined />} onClick={handleDownload}>
            Download
          </Button>
          <Button icon={<CopyOutlined />} onClick={handleClone}>
            Clone
          </Button>
        </Space>
      </div>

      {/* ---- Info Card ---- */}
      <Card title='Details' style={{ marginBottom: 24 }}>
        <Descriptions column={2} bordered size='small'>
          <Descriptions.Item label='Fault Type'>
            {datapack.fault_type || '-'}
          </Descriptions.Item>
          <Descriptions.Item label='Benchmark'>
            {datapack.benchmark_name || '-'}
          </Descriptions.Item>
          <Descriptions.Item label='Pedestal'>
            {datapack.pedestal_name || '-'}
          </Descriptions.Item>
          <Descriptions.Item label='Created'>
            {datapack.created_at
              ? dayjs(datapack.created_at).format('YYYY-MM-DD HH:mm:ss')
              : '-'}
          </Descriptions.Item>
        </Descriptions>
      </Card>

      {/* ---- Task Logs ---- */}
      {datapack.task_id && (
        <Card title='Task Logs' style={{ marginBottom: 24 }}>
          <TaskLogViewer taskId={datapack.task_id} />
        </Card>
      )}

      {/* ---- Files ---- */}
      {treeData.length > 0 && (
        <Card title='Files'>
          <Tree
            showIcon
            defaultExpandAll
            treeData={treeData}
            selectable={false}
          />
        </Card>
      )}
    </div>
  );
};

export default DatapackDetail;
