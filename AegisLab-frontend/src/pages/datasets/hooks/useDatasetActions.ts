import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { message, Modal } from 'antd';

import { datasetApi } from '@/api/datasets';

export function useDatasetActions(refetch: () => void) {
  const navigate = useNavigate();
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [uploadModalVisible, setUploadModalVisible] = useState(false);
  const [uploadingFile, setUploadingFile] = useState<File | null>(null);
  const [uploadProgress, setUploadProgress] = useState(0);

  const handleCreateDataset = useCallback(
    () => navigate('/datasets/new'),
    [navigate]
  );

  const handleViewDataset = useCallback(
    (id: number) => navigate(`/datasets/${id}`),
    [navigate]
  );

  const handleEditDataset = useCallback(
    (id: number) => navigate(`/datasets/${id}/edit`),
    [navigate]
  );

  const handleManageVersions = useCallback(
    (id: number) => navigate(`/datasets/${id}/versions`),
    [navigate]
  );

  const handleDeleteDataset = useCallback(
    (id: number) => {
      Modal.confirm({
        title: 'Delete Dataset',
        content:
          'Are you sure you want to delete this dataset? This action cannot be undone.',
        okText: 'Yes, delete it',
        okButtonProps: { danger: true },
        cancelText: 'Cancel',
        onOk: async () => {
          try {
            await datasetApi.deleteDataset(id);
            message.success('Dataset deleted successfully');
            refetch();
          } catch {
            message.error('Failed to delete dataset');
          }
        },
      });
    },
    [refetch]
  );

  const handleBatchDelete = useCallback(() => {
    if (selectedRowKeys.length === 0) {
      message.warning('Please select datasets to delete');
      return;
    }

    Modal.confirm({
      title: 'Batch Delete Datasets',
      content: `Are you sure you want to delete ${selectedRowKeys.length} datasets?`,
      okText: 'Yes, delete them',
      okButtonProps: { danger: true },
      cancelText: 'Cancel',
      onOk: async () => {
        try {
          await Promise.all(
            (selectedRowKeys as number[]).map((deleteId) =>
              datasetApi.deleteDataset(deleteId)
            )
          );
          message.success(
            `${selectedRowKeys.length} datasets deleted successfully`
          );
          setSelectedRowKeys([]);
          refetch();
        } catch {
          message.error('Failed to delete datasets');
        }
      },
    });
  }, [selectedRowKeys, refetch]);

  const handleUploadDataset = useCallback(
    () => setUploadModalVisible(true),
    []
  );

  const handleFileSelect = useCallback((file: File) => {
    setUploadingFile(file);
    return false; // Prevent auto upload
  }, []);

  const handleUpload = useCallback(async () => {
    if (!uploadingFile) return;
    message.info('File upload is not yet supported');
    setUploadModalVisible(false);
    setUploadingFile(null);
    setUploadProgress(0);
  }, [uploadingFile]);

  const handleCloseUploadModal = useCallback(() => {
    setUploadModalVisible(false);
    setUploadingFile(null);
    setUploadProgress(0);
  }, []);

  return {
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
  };
}
