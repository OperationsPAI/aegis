import { UserOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Avatar, Card, Descriptions, Skeleton, Typography } from 'antd';
import dayjs from 'dayjs';

import { authApi } from '@/api/auth';

import './ProfilePage.css';

const { Text, Title } = Typography;

const ProfilePage = () => {
  const { data: profile, isLoading } = useQuery({
    queryKey: ['profile'],
    queryFn: () => authApi.getProfile(),
  });

  if (isLoading) {
    return (
      <div className='profile-page'>
        <div className='profile-content'>
          <Skeleton active avatar={{ size: 64 }} paragraph={{ rows: 6 }} />
        </div>
      </div>
    );
  }

  return (
    <div className='profile-page'>
      <div className='profile-content'>
        <Card>
          <div className='profile-header'>
            <Avatar size={64} icon={<UserOutlined />} src={profile?.avatar} />
            <div>
              <Title level={4} style={{ margin: 0 }}>
                {profile?.full_name || profile?.username || 'User'}
              </Title>
              <Text type='secondary'>@{profile?.username}</Text>
            </div>
          </div>

          <Alert
            message='Profile information is read-only'
            description='Contact your administrator to update profile details.'
            type='info'
            showIcon
            style={{ marginTop: 24, marginBottom: 24 }}
          />

          {profile ? (
            <Descriptions bordered column={{ xs: 1, sm: 2 }}>
              <Descriptions.Item label='Username'>
                @{profile.username}
              </Descriptions.Item>
              <Descriptions.Item label='Full Name'>
                {profile.full_name || '-'}
              </Descriptions.Item>
              <Descriptions.Item label='Email'>
                {profile.email}
              </Descriptions.Item>
              <Descriptions.Item label='Phone'>
                {profile.phone || '-'}
              </Descriptions.Item>
              <Descriptions.Item label='Role'>
                {profile.global_roles?.map((r) => r.name).join(', ') || '-'}
              </Descriptions.Item>
              <Descriptions.Item label='Member Since'>
                {profile.created_at
                  ? dayjs(profile.created_at).format('MMMM D, YYYY')
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label='Last Login'>
                {profile.last_login_at
                  ? dayjs(profile.last_login_at).format('MMMM D, YYYY HH:mm')
                  : 'Never'}
              </Descriptions.Item>
            </Descriptions>
          ) : (
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                padding: '40px 0',
                color: 'var(--color-secondary-500)',
              }}
            >
              <UserOutlined
                style={{
                  fontSize: 48,
                  marginBottom: 16,
                  color: 'var(--color-secondary-300)',
                }}
              />
              <Text strong style={{ fontSize: 16 }}>
                Unable to load profile
              </Text>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
};

export default ProfilePage;
