import {
  BarChartOutlined,
  DatabaseOutlined,
  FunctionOutlined,
} from '@ant-design/icons';
import {
  Card,
  Col,
  Divider,
  Row,
  Space,
  Statistic,
  Tag,
  Typography,
} from 'antd';

const { Text } = Typography;

interface MetricsSectionProps {
  algorithmsCount: number;
  datapacksCount: number;
  datasetsCount: number;
}

const MetricsSection: React.FC<MetricsSectionProps> = ({
  algorithmsCount,
  datapacksCount,
  datasetsCount,
}) => {
  return (
    <>
      <Card
        title={
          <Space>
            <BarChartOutlined />
            <span>Evaluation Guide</span>
          </Space>
        }
      >
        <Space direction='vertical' style={{ width: '100%' }}>
          <div>
            <Text strong>Evaluation Types:</Text>
            <ul style={{ marginTop: 8, marginBottom: 16 }}>
              <li>
                <Text>
                  <strong>Datapack Evaluation:</strong> Test algorithm
                  performance on real experiment data collected from fault
                  injections
                </Text>
              </li>
              <li>
                <Text>
                  <strong>Dataset Evaluation:</strong> Test algorithm
                  performance on standard benchmark datasets
                </Text>
              </li>
            </ul>
          </div>

          <Divider />

          <div>
            <Text strong>Metrics:</Text>
            <ul style={{ marginTop: 8, marginBottom: 16 }}>
              <li>
                <Text>
                  <strong>Precision:</strong> Accuracy of positive predictions
                </Text>
              </li>
              <li>
                <Text>
                  <strong>Recall:</strong> Coverage of actual positive cases
                </Text>
              </li>
              <li>
                <Text>
                  <strong>F1-Score:</strong> Harmonic mean of precision and
                  recall
                </Text>
              </li>
              <li>
                <Text>
                  <strong>Accuracy:</strong> Overall correctness of predictions
                </Text>
              </li>
            </ul>
          </div>

          <Divider />

          <div>
            <Text strong>Best Practices:</Text>
            <ul style={{ marginTop: 8 }}>
              <li>
                <Text>Use consistent datasets for fair comparison</Text>
              </li>
              <li>
                <Text>Include groundtruth data when available</Text>
              </li>
              <li>
                <Text>
                  Run multiple evaluations for statistical significance
                </Text>
              </li>
              <li>
                <Text>Document evaluation parameters and conditions</Text>
              </li>
            </ul>
          </div>

          <Divider />

          <div>
            <Text strong>Performance Benchmarks:</Text>
            <div style={{ marginTop: 8 }}>
              <Tag color='green'>Excellent: F1 &ge; 0.9</Tag>
              <br />
              <Tag color='orange'>Good: 0.7 &le; F1 &lt; 0.9</Tag>
              <br />
              <Tag color='red'>Needs Improvement: F1 &lt; 0.7</Tag>
            </div>
          </div>
        </Space>
      </Card>

      <Card title='Quick Stats' style={{ marginTop: 16 }}>
        <Row gutter={[16, 16]}>
          <Col span={12}>
            <Statistic
              title='Available Algorithms'
              value={algorithmsCount}
              prefix={<FunctionOutlined />}
              valueStyle={{ color: 'var(--color-warning)' }}
            />
          </Col>
          <Col span={12}>
            <Statistic
              title='Available Datapacks'
              value={datapacksCount}
              prefix={<DatabaseOutlined />}
              valueStyle={{ color: 'var(--color-primary-500)' }}
            />
          </Col>
          <Col span={12}>
            <Statistic
              title='Available Datasets'
              value={datasetsCount}
              prefix={<DatabaseOutlined />}
              valueStyle={{ color: 'var(--color-success)' }}
            />
          </Col>
          <Col span={12}>
            <Statistic
              title='Total Evaluations'
              value='&infin;'
              prefix={<BarChartOutlined />}
              valueStyle={{ color: 'var(--color-primary-700)' }}
            />
          </Col>
        </Row>
      </Card>
    </>
  );
};

export default MetricsSection;
