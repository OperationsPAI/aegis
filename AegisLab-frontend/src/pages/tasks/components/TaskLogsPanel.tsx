import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Card, Empty, Space } from 'antd';

interface TaskLogsPanelProps {
  logs: string[];
  onClear: () => void;
  onDownload: () => void;
}

const TaskLogsPanel: React.FC<TaskLogsPanelProps> = ({
  logs,
  onClear,
  onDownload,
}) => {
  return (
    <Card
      title='Task Logs'
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={onClear}>
            Clear Logs
          </Button>
          <Button
            icon={<DownloadOutlined />}
            onClick={onDownload}
            disabled={logs.length === 0}
          >
            Download
          </Button>
        </Space>
      }
    >
      {logs.length > 0 ? (
        <div
          style={{
            background: 'var(--color-secondary-100)',
            padding: 16,
            borderRadius: 4,
            maxHeight: 400,
            overflow: 'auto',
          }}
        >
          <pre
            style={{
              margin: 0,
              fontSize: '0.875rem',
              fontFamily: 'monospace',
            }}
          >
            {logs.join('\n')}
          </pre>
        </div>
      ) : (
        <Empty
          description='No logs available. Logs will appear when the task starts running.'
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
      )}
    </Card>
  );
};

export default TaskLogsPanel;
