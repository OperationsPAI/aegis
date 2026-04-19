import { useState } from 'react';

import {
  CloudUploadOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  LineChartOutlined,
  PlusOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Card,
  Col,
  Modal,
  Progress,
  Row,
  Space,
  Table,
  type TablePaginationConfig,
  Typography,
  Upload,
} from 'antd';

import { datasetApi } from '@/api/datasets';
import StatCard from '@/components/ui/StatCard';
import { usePagination } from '@/hooks/usePagination';

import { buildDatasetColumns } from './columns/datasetColumns';
import DatasetFilters from './components/DatasetFilters';
import { useDatasetActions } from './hooks/useDatasetActions';

type DatasetType = 'Trace' | 'Log' | 'Metric';

const { Title, Text } = Typography;

const formatFileSize = (bytes: number) => {
  if (bytes === 0) return '0 Bytes';
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`;
};

const DatasetList = () => {
  const [searchText, setSearchText] = useState('');
  const [typeFilter, setTypeFilter] = useState<DatasetType | undefined>();
  const {
    current,
    pageSize,
    onChange: onPaginationChange,
    reset: resetPagination,
  } = usePagination({ defaultPageSize: 10 });

  const {
    data: datasetsData,
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ['datasets', current, pageSize, searchText, typeFilter],
    queryFn: () =>
      datasetApi.getDatasets({
        page: current,
        size: pageSize,
        type: typeFilter,
      }),
  });

  const {
    selectedRowKeys,
    setSelectedRowKeys,
    uploadModalVisible,
    uploadingFile,
    uploadProgress,
    handleCreateDataset,
    handleViewDataset,
    handleEditDataset,
    handleManageVersions,
    handleDeleteDataset,
    handleBatchDelete,
    handleUploadDataset,
    handleFileSelect,
    handleUpload,
    handleCloseUploadModal,
  } = useDatasetActions(refetch);

  const stats = {
    total: datasetsData?.pagination?.total || 0,
    trace: datasetsData?.items?.filter((d) => d.type === 'Trace').length || 0,
    log: datasetsData?.items?.filter((d) => d.type === 'Log').length || 0,
    metric: datasetsData?.items?.filter((d) => d.type === 'Metric').length || 0,
  };

  const handleTableChange = (newPagination: TablePaginationConfig) => {
    onPaginationChange(
      newPagination.current || 1,
      newPagination.pageSize || 10
    );
  };

  const handleSearch = (value: string) => {
    setSearchText(value);
    resetPagination();
  };

  const handleTypeFilter = (type: DatasetType | undefined) => {
    setTypeFilter(type);
    resetPagination();
  };

  const columns = buildDatasetColumns(
    handleViewDataset,
    handleEditDataset,
    handleManageVersions,
    handleDeleteDataset
  );

  return (
    <div className='dataset-list'>
      {/* Page Header */}
      <div className='page-header'>
        <div className='page-header-left'>
          <Title level={4} className='page-title'>
            Dataset Management
          </Title>
          <Text type='secondary'>
            Manage your Trace, Log, and Metric datasets
          </Text>
        </div>
        <div className='page-header-right'>
          <Space>
            <Button
              icon={<CloudUploadOutlined />}
              size='large'
              onClick={handleUploadDataset}
            >
              Upload Dataset
            </Button>
            <Button
              type='primary'
              size='large'
              icon={<PlusOutlined />}
              onClick={handleCreateDataset}
            >
              Create Dataset
            </Button>
          </Space>
        </div>
      </div>

      {/* Statistics Cards */}
      <Row
        gutter={[
          { xs: 8, sm: 16, lg: 24 },
          { xs: 8, sm: 16, lg: 24 },
        ]}
        className='stats-row'
      >
        <Col xs={12} sm={12} lg={6}>
          <StatCard
            title='Total Datasets'
            value={stats.total}
            prefix={<DatabaseOutlined />}
            color='primary'
          />
        </Col>
        <Col xs={12} sm={12} lg={6}>
          <StatCard
            title='Trace Datasets'
            value={stats.trace}
            prefix={<DatabaseOutlined />}
            color='primary'
          />
        </Col>
        <Col xs={12} sm={12} lg={6}>
          <StatCard
            title='Log Datasets'
            value={stats.log}
            prefix={<FileTextOutlined />}
            color='success'
          />
        </Col>
        <Col xs={12} sm={12} lg={6}>
          <StatCard
            title='Metric Datasets'
            value={stats.metric}
            prefix={<LineChartOutlined />}
            color='warning'
          />
        </Col>
      </Row>

      {/* Filters */}
      <DatasetFilters
        typeFilter={typeFilter}
        selectedCount={selectedRowKeys.length}
        onSearch={handleSearch}
        onTypeFilter={handleTypeFilter}
        onBatchDelete={handleBatchDelete}
        onUpload={handleUploadDataset}
      />

      {/* Dataset Table */}
      <Card className='table-card'>
        <Table
          rowKey='id'
          rowSelection={{ selectedRowKeys, onChange: setSelectedRowKeys }}
          columns={columns}
          dataSource={datasetsData?.items || []}
          loading={isLoading}
          className='datasets-table'
          pagination={{
            current,
            pageSize,
            total: datasetsData?.pagination?.total || 0,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total, range) =>
              `${range[0]}-${range[1]} of ${total} datasets`,
          }}
          onChange={handleTableChange}
        />
      </Card>

      {/* Upload Modal */}
      <Modal
        title='Upload Dataset'
        open={uploadModalVisible}
        onCancel={handleCloseUploadModal}
        footer={[
          <Button key='cancel' onClick={handleCloseUploadModal}>
            Cancel
          </Button>,
          <Button
            key='upload'
            type='primary'
            icon={<UploadOutlined />}
            onClick={handleUpload}
            disabled={!uploadingFile || uploadProgress > 0}
            loading={uploadProgress > 0}
          >
            Upload
          </Button>,
        ]}
      >
        <Upload.Dragger
          accept='.csv,.json,.parquet,.zip'
          maxCount={1}
          beforeUpload={handleFileSelect}
          showUploadList={false}
        >
          <p className='ant-upload-drag-icon'>
            <CloudUploadOutlined
              style={{ fontSize: 48, color: 'var(--color-primary-500)' }}
            />
          </p>
          <p className='ant-upload-text'>
            Click or drag dataset file to this area
          </p>
          <p className='ant-upload-hint'>
            Support for single file upload. File types: .csv, .json, .parquet,
            .zip
          </p>
        </Upload.Dragger>
        {uploadingFile && (
          <div style={{ marginTop: 16 }}>
            <Text strong>Selected file: </Text>
            <Text>{uploadingFile.name}</Text>
            <br />
            <Text type='secondary'>
              Size: {formatFileSize(uploadingFile.size)}
            </Text>
            {uploadProgress > 0 && (
              <Progress
                percent={uploadProgress}
                status={uploadProgress === 100 ? 'success' : 'active'}
                style={{ marginTop: 8 }}
              />
            )}
          </div>
        )}
      </Modal>
    </div>
  );
};

export default DatasetList;
