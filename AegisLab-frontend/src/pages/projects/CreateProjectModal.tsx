import type { ProjectResp } from '@rcabench/client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Form, Input, message, Modal, Switch } from 'antd';

import { projectApi } from '@/api/projects';

interface CreateProjectModalProps {
  open: boolean;
  onClose: () => void;
  onSuccess: (project: ProjectResp) => void;
}

const CreateProjectModal = ({
  open,
  onClose,
  onSuccess,
}: CreateProjectModalProps) => {
  const [form] = Form.useForm();
  const queryClient = useQueryClient();

  const createMutation = useMutation({
    mutationFn: (values: {
      name: string;
      description?: string;
      is_public?: boolean;
    }) => projectApi.createProject(values),
    onSuccess: (project) => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      message.success('Project created successfully');
      form.resetFields();
      if (project) {
        onSuccess(project);
      }
    },
    onError: () => {
      message.error('Failed to create project');
    },
  });

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      createMutation.mutate({
        name: values.name,
        description: values.description,
        is_public: values.is_public ?? false,
      });
    } catch {
      // validation error, form will show messages
    }
  };

  const handleCancel = () => {
    form.resetFields();
    onClose();
  };

  return (
    <Modal
      title='New Project'
      open={open}
      onOk={handleSubmit}
      onCancel={handleCancel}
      okText='Create'
      confirmLoading={createMutation.isPending}
      destroyOnClose
    >
      <Form form={form} layout='vertical' style={{ marginTop: 16 }}>
        <Form.Item
          name='name'
          label='Name'
          rules={[{ required: true, message: 'Please enter a project name' }]}
        >
          <Input placeholder='my-project' />
        </Form.Item>

        <Form.Item name='description' label='Description'>
          <Input.TextArea rows={3} placeholder='Optional project description' />
        </Form.Item>

        <Form.Item
          name='is_public'
          label='Public'
          valuePropName='checked'
          initialValue={false}
        >
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default CreateProjectModal;
