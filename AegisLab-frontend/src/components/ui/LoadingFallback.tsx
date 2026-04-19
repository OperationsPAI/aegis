import { Spin } from 'antd';

const LoadingFallback: React.FC = () => (
  <div
    style={{
      display: 'flex',
      justifyContent: 'center',
      alignItems: 'center',
      height: '100%',
      minHeight: 200,
    }}
  >
    <Spin size='large' />
  </div>
);

export default LoadingFallback;
