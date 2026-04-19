import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { DeleteOutlined, SaveOutlined } from '@ant-design/icons';
import type { ProjectDetailResp } from '@rcabench/client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Form,
  Input,
  message,
  Modal,
  Space,
  Switch,
  Typography,
} from 'antd';

import { projectApi } from '@/api/projects';

const { Text, Paragraph } = Typography;
const { TextArea } = Input;

/** Extended fields that may come from the API but are not in the SDK type */
type ProjectWithExtras = ProjectDetailResp & {
  description?: string;
};

interface SettingsTabProps {
  project: ProjectDetailResp;
  projectId: number;
}

/**
 * Project settings tab with edit form and danger zone.
 */
const SettingsTab: React.FC<SettingsTabProps> = ({
  project: rawProject,
  projectId,
}) => {
  const project = rawProject as ProjectWithExtras;
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [deleteConfirmText, setDeleteConfirmText] = useState('');

  // Update project mutation
  const updateMutation = useMutation({
    mutationFn: (data: {
      name?: string;
      description?: string;
      is_public?: boolean;
    }) => projectApi.updateProject(projectId, data),
    onSuccess: () => {
      message.success('Project updated successfully');
      queryClient.invalidateQueries({ queryKey: ['project', projectId] });
    },
    onError: () => {
      message.error('Failed to update project');
    },
  });

  // Delete project mutation
  const deleteMutation = useMutation({
    mutationFn: () => projectApi.deleteProject(projectId),
    onSuccess: () => {
      message.success('Project deleted successfully');
      navigate('/projects');
    },
    onError: () => {
      message.error('Failed to delete project');
    },
  });

  const handleSubmit = (values: {
    name: string;
    description: string;
    is_public: boolean;
  }) => {
    updateMutation.mutate(values);
  };

  const handleDelete = () => {
    deleteMutation.mutate();
  };

  const handleCloseDeleteModal = () => {
    setDeleteModalOpen(false);
    setDeleteConfirmText('');
  };

  return (
    <div>
      {/* General Settings */}
      <Card title='General' style={{ marginBottom: 24 }}>
        <Form
          form={form}
          layout='vertical'
          initialValues={{
            name: project.name,
            description: project.description ?? '',
            is_public: project.is_public ?? false,
          }}
          onFinish={handleSubmit}
        >
          <Form.Item
            name='name'
            label='Project Name'
            rules={[{ required: true, message: 'Please enter project name' }]}
          >
            <Input placeholder='Enter project name' />
          </Form.Item>

          <Form.Item name='description' label='Description'>
            <TextArea rows={4} placeholder='Enter project description' />
          </Form.Item>

          <Form.Item
            name='is_public'
            label='Public Project'
            valuePropName='checked'
          >
            <Switch />
          </Form.Item>

          <Form.Item>
            <Button
              type='primary'
              htmlType='submit'
              icon={<SaveOutlined />}
              loading={updateMutation.isPending}
            >
              Save Changes
            </Button>
          </Form.Item>
        </Form>
      </Card>

      {/* Danger Zone */}
      <Card
        title={<Text type='danger'>Danger Zone</Text>}
        styles={{ header: { borderBottom: '1px solid var(--color-error)' } }}
      >
        <Space direction='vertical' style={{ width: '100%' }}>
          <div>
            <Text strong>Delete this project</Text>
            <br />
            <Text type='secondary'>
              Once you delete a project, there is no going back. Please be
              certain.
            </Text>
          </div>
          <Button
            danger
            icon={<DeleteOutlined />}
            onClick={() => setDeleteModalOpen(true)}
          >
            Delete Project
          </Button>
        </Space>
      </Card>

      {/* Delete Confirmation Modal */}
      <Modal
        title={`Delete "${project.name}" project?`}
        open={deleteModalOpen}
        onCancel={handleCloseDeleteModal}
        footer={[
          <Button key='cancel' onClick={handleCloseDeleteModal}>
            Cancel
          </Button>,
          <Button
            key='delete'
            danger
            type='primary'
            disabled={deleteConfirmText !== project.name}
            loading={deleteMutation.isPending}
            onClick={handleDelete}
          >
            Delete
          </Button>,
        ]}
      >
        <div>
          <Paragraph>
            This will permanently delete {project.name} and all associated data.{' '}
            <Text strong>This action cannot be undone.</Text>
          </Paragraph>
          <Paragraph style={{ marginBottom: 8 }}>
            Please type <Text strong>{project.name}</Text> to confirm.
          </Paragraph>
          <Input
            value={deleteConfirmText}
            onChange={(e) => setDeleteConfirmText(e.target.value)}
            placeholder={project.name}
          />
        </div>
      </Modal>
    </div>
  );
};

export default SettingsTab;
