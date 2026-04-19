import { useState } from 'react';

import {
  EyeInvisibleOutlined,
  EyeOutlined,
  LockOutlined,
  MailOutlined,
  PhoneOutlined,
  SaveOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Avatar,
  Button,
  Card,
  Col,
  Descriptions,
  Form,
  Input,
  message,
  Row,
  Skeleton,
  Space,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';

import { authApi } from '../../api/auth';
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard';

const { Title, Text } = Typography;

const Settings = () => {
  const [activeTab, setActiveTab] = useState('profile');
  const [securityForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [isDirty, setIsDirty] = useState(false);
  useUnsavedChangesGuard(isDirty);

  // Fetch user data with TanStack Query
  const {
    data: userData,
    isLoading,
    error,
  } = useQuery({
    queryKey: ['profile'],
    queryFn: () => authApi.getProfile(),
  });

  const handleChangePassword = async (values: {
    oldPassword: string;
    newPassword: string;
  }) => {
    setLoading(true);
    try {
      await authApi.changePassword({
        old_password: values.oldPassword,
        new_password: values.newPassword,
      });
      message.success('Password changed successfully');
      setIsDirty(false);
      securityForm.resetFields();
    } catch {
      // Error handled by apiClient interceptor
    } finally {
      setLoading(false);
    }
  };

  if (isLoading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active avatar paragraph={{ rows: 10 }} />
      </div>
    );
  }

  if (error || !userData) {
    return (
      <div style={{ padding: 24 }}>
        <Card>
          <Text type='danger'>Failed to load user settings</Text>
        </Card>
      </div>
    );
  }

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <Title level={4} style={{ margin: 0 }}>
          <SettingOutlined style={{ marginRight: 8 }} />
          Settings
        </Title>
        <Text type='secondary'>
          Manage your account settings and preferences
        </Text>
      </div>

      {/* Profile Overview */}
      <Card style={{ marginBottom: 24 }}>
        <Row gutter={[24, 24]} align='middle'>
          <Col xs={24} sm={6} md={4}>
            <div style={{ textAlign: 'center' }}>
              <Avatar
                size={96}
                icon={<UserOutlined />}
                src={userData.avatar}
                style={{ backgroundColor: 'var(--color-primary-500)' }}
              />
              <div style={{ marginTop: 16 }}>
                <Title level={4} style={{ margin: 0 }}>
                  {userData.full_name || userData.username}
                </Title>
                <Text type='secondary'>@{userData.username}</Text>
              </div>
            </div>
          </Col>
          <Col xs={24} sm={18} md={20}>
            <Descriptions
              title='Account Overview'
              bordered
              column={{ xs: 1, sm: 2, md: 3 }}
            >
              <Descriptions.Item label='Email'>
                <Space>
                  <MailOutlined />
                  {userData.email}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label='Phone'>
                <Space>
                  <PhoneOutlined />
                  {userData.phone || '-'}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label='Status'>
                <Tag color={userData.is_active ? 'green' : 'orange'}>
                  {userData.is_active ? 'Active' : 'Inactive'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label='Member Since'>
                {userData.created_at
                  ? dayjs(userData.created_at).format('MMMM D, YYYY')
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label='Last Login'>
                {userData.last_login_at
                  ? dayjs(userData.last_login_at).format('MMMM D, YYYY HH:mm')
                  : 'Never'}
              </Descriptions.Item>
            </Descriptions>
          </Col>
        </Row>
      </Card>

      {/* Settings Tabs */}
      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'profile',
            label: (
              <span>
                <UserOutlined />
                Profile
              </span>
            ),
            children: (
              <Card title='Profile Information'>
                <Alert
                  message='Profile is read-only'
                  description='Profile editing is managed by your administrator. Contact your admin to update your profile information.'
                  type='info'
                  showIcon
                  style={{ marginBottom: 24 }}
                />
                <Descriptions bordered column={{ xs: 1, sm: 2 }}>
                  <Descriptions.Item label='Full Name'>
                    <Space>
                      <UserOutlined />
                      {userData.full_name || '-'}
                    </Space>
                  </Descriptions.Item>
                  <Descriptions.Item label='Username'>
                    @{userData.username}
                  </Descriptions.Item>
                  <Descriptions.Item label='Email'>
                    <Space>
                      <MailOutlined />
                      {userData.email}
                    </Space>
                  </Descriptions.Item>
                  <Descriptions.Item label='Phone'>
                    <Space>
                      <PhoneOutlined />
                      {userData.phone || '-'}
                    </Space>
                  </Descriptions.Item>
                  <Descriptions.Item label='Status'>
                    <Tag color={userData.is_active ? 'green' : 'orange'}>
                      {userData.is_active ? 'Active' : 'Inactive'}
                    </Tag>
                  </Descriptions.Item>
                  <Descriptions.Item label='Member Since'>
                    {userData.created_at
                      ? dayjs(userData.created_at).format('MMMM D, YYYY')
                      : '-'}
                  </Descriptions.Item>
                </Descriptions>
              </Card>
            ),
          },

          {
            key: 'security',
            label: (
              <span>
                <LockOutlined />
                Security
              </span>
            ),
            children: (
              <Card title='Security Settings'>
                <Form
                  form={securityForm}
                  layout='vertical'
                  onFinish={handleChangePassword}
                  onValuesChange={() => setIsDirty(true)}
                >
                  <Title level={4}>Change Password</Title>
                  <Row gutter={[24, 24]}>
                    <Col xs={24} lg={12}>
                      <Form.Item
                        label='Current Password'
                        name='oldPassword'
                        rules={[
                          {
                            required: true,
                            message: 'Please enter current password',
                          },
                        ]}
                      >
                        <Input.Password
                          placeholder='Enter current password'
                          iconRender={(visible) =>
                            visible ? <EyeOutlined /> : <EyeInvisibleOutlined />
                          }
                        />
                      </Form.Item>

                      <Form.Item
                        label='New Password'
                        name='newPassword'
                        rules={[
                          {
                            required: true,
                            message: 'Please enter new password',
                          },
                          {
                            min: 8,
                            message: 'Password must be at least 8 characters',
                          },
                        ]}
                      >
                        <Input.Password
                          placeholder='Enter new password'
                          iconRender={(visible) =>
                            visible ? <EyeOutlined /> : <EyeInvisibleOutlined />
                          }
                        />
                      </Form.Item>

                      <Form.Item
                        label='Confirm New Password'
                        name='confirmPassword'
                        dependencies={['newPassword']}
                        rules={[
                          {
                            required: true,
                            message: 'Please confirm new password',
                          },
                          ({ getFieldValue }) => ({
                            validator(_, value) {
                              if (
                                !value ||
                                getFieldValue('newPassword') === value
                              ) {
                                return Promise.resolve();
                              }
                              return Promise.reject(
                                new Error('Passwords do not match')
                              );
                            },
                          }),
                        ]}
                      >
                        <Input.Password
                          placeholder='Confirm new password'
                          iconRender={(visible) =>
                            visible ? <EyeOutlined /> : <EyeInvisibleOutlined />
                          }
                        />
                      </Form.Item>

                      <Form.Item>
                        <Button
                          type='primary'
                          icon={<SaveOutlined />}
                          loading={loading}
                          htmlType='submit'
                        >
                          Change Password
                        </Button>
                      </Form.Item>
                    </Col>

                    <Col xs={24} lg={12}>
                      <Alert
                        message='Password Requirements'
                        description='Your password must be at least 8 characters long.'
                        type='info'
                        showIcon
                      />
                    </Col>
                  </Row>
                </Form>
              </Card>
            ),
          },
        ]}
      />
    </div>
  );
};

export default Settings;
