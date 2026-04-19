import { Form, Input, Modal } from 'antd';

interface CreateRoleModalProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (values: {
    name: string;
    display_name: string;
    description?: string;
  }) => void;
  loading?: boolean;
}

const CreateRoleModal: React.FC<CreateRoleModalProps> = ({
  open,
  onClose,
  onSubmit,
  loading,
}) => {
  const [createForm] = Form.useForm();

  const handleClose = () => {
    onClose();
    createForm.resetFields();
  };

  return (
    <Modal
      title='Create Role'
      open={open}
      onCancel={handleClose}
      onOk={() => createForm.submit()}
      okText='Create'
      confirmLoading={loading}
    >
      <Form
        form={createForm}
        layout='vertical'
        onFinish={(values) => {
          onSubmit(values);
          createForm.resetFields();
        }}
      >
        <Form.Item
          name='name'
          label='Name'
          rules={[
            { required: true, message: 'Please enter a role name' },
            {
              pattern: /^[a-z][a-z0-9_-]*$/,
              message:
                'Name must start with a lowercase letter and contain only lowercase letters, numbers, hyphens, and underscores',
            },
          ]}
        >
          <Input placeholder='e.g., project-manager' />
        </Form.Item>
        <Form.Item
          name='display_name'
          label='Display Name'
          rules={[{ required: true, message: 'Please enter a display name' }]}
        >
          <Input placeholder='e.g., Project Manager' />
        </Form.Item>
        <Form.Item name='description' label='Description'>
          <Input.TextArea
            rows={3}
            placeholder='Optional description of this role'
          />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default CreateRoleModal;
