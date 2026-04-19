import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { LockOutlined, UserOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, message, Typography } from 'antd';

import { useAuthStore } from '@/store/auth';

const { Title, Text } = Typography;

const Login = () => {
  const navigate = useNavigate();
  const { login } = useAuthStore();
  const [loading, setLoading] = useState(false);

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true);
    try {
      await login(values.username, values.password);
      message.success('Login successful');
      // Add a small delay to ensure state is updated
      setTimeout(() => {
        navigate('/home');
      }, 100);
    } catch (error) {
      message.error('Login failed. Please check your username and password.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background:
          'linear-gradient(135deg, var(--color-primary-600) 0%, var(--color-primary-800) 100%)',
      }}
    >
      <Card
        style={{
          width: 420,
          boxShadow: '0 20px 25px -5px var(--color-shadow, rgb(0 0 0 / 0.1))',
        }}
      >
        <div style={{ textAlign: 'center', marginBottom: '32px' }}>
          <div
            style={{
              fontSize: '48px',
              marginBottom: '16px',
            }}
          >
            🔬
          </div>
          <Title level={4} style={{ marginBottom: '8px' }}>
            RCABench
          </Title>
          <Text type='secondary'>
            Microservice Root Cause Analysis Benchmark Platform
          </Text>
        </div>

        <Form name='login' size='large' onFinish={onFinish} autoComplete='off'>
          <Form.Item
            name='username'
            rules={[{ required: true, message: 'Please enter your username' }]}
          >
            <Input prefix={<UserOutlined />} placeholder='Username' />
          </Form.Item>

          <Form.Item
            name='password'
            rules={[{ required: true, message: 'Please enter your password' }]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder='Password' />
          </Form.Item>

          <Form.Item>
            <Button
              type='primary'
              htmlType='submit'
              block
              loading={loading}
              size='large'
            >
              Sign In
            </Button>
          </Form.Item>

          <div style={{ textAlign: 'center' }}>
            <Text type='secondary' style={{ fontSize: '12px' }}>
              AegisLab - Root Cause Analysis Benchmark Platform
            </Text>
          </div>
        </Form>
      </Card>
    </div>
  );
};

export default Login;
