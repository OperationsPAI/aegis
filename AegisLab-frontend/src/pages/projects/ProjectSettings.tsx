import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { DeleteOutlined, SaveOutlined } from '@ant-design/icons';
import type { ProjectDetailResp } from '@rcabench/client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button,
  Card,
  Form,
  Input,
  message,
  Modal,
  Skeleton,
  Space,
  Switch,
  Typography,
} from 'antd';

import { projectApi } from '@/api/projects';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';

import ProjectSubNav from './ProjectSubNav';

const { Text, Paragraph } = Typography;
const { TextArea } = Input;

type ProjectWithExtras = ProjectDetailResp & {
  description?: string;
};

/**
 * Project settings page with edit form and danger zone.
 * Route: /projects/:id/settings
 */
const ProjectSettings: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const projectId = Number(id);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [deleteConfirmText, setDeleteConfirmText] = useState('');
  const [isDirty, setIsDirty] = useState(false);
  useUnsavedChangesGuard(isDirty);

  const { data: rawProject, isLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => projectApi.getProjectDetail(projectId),
    enabled: !!projectId && !Number.isNaN(projectId),
  });

  const project = rawProject as ProjectWithExtras | undefined;

  const updateMutation = useMutation({
    mutationFn: (data: {
      name?: string;
      description?: string;
      is_public?: boolean;
    }) => projectApi.updateProject(projectId, data),
    onSuccess: () => {
      message.success('Project updated successfully');
      setIsDirty(false);
      queryClient.invalidateQueries({ queryKey: ['project', projectId] });
    },
    onError: () => {
      message.error('Failed to update project');
    },
  });

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

  if (isLoading || !project) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    );
  }

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
    <div style={{ padding: 24 }}>
      <ProjectSubNav projectId={projectId} activeKey='settings' />

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
          onValuesChange={() => setIsDirty(true)}
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

export default ProjectSettings;
