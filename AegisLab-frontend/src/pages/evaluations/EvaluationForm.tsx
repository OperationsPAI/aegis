import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import {
  BarChartOutlined,
  CloseOutlined,
  InfoCircleOutlined,
  PlayCircleOutlined,
} from '@ant-design/icons';
import type {
  EvaluateDatapackSpec,
  EvaluateDatasetSpec,
} from '@rcabench/client';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Col,
  Empty,
  Form,
  Input,
  message,
  Progress,
  Row,
  Space,
  Typography,
} from 'antd';

import { containerApi } from '@/api/containers';
import { datasetApi } from '@/api/datasets';
import { evaluationApi } from '@/api/evaluations';
import { executionApi } from '@/api/executions';

import AlgorithmsSection from './sections/AlgorithmsSection';
import DatasetsSection from './sections/DatasetsSection';
import EvaluationTypeSelect from './sections/EvaluationTypeSelect';
import MetricsSection from './sections/MetricsSection';

const { Title, Text } = Typography;
const { TextArea } = Input;

interface EvaluationFormData {
  algorithm_name: string;
  algorithm_version: string;
  datapack_id: string;
  dataset_id?: string;
  groundtruth_dataset_id?: string;
  notes?: string;
}

const EvaluationForm = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<EvaluationFormData>();
  const [selectedAlgorithm, setSelectedAlgorithm] = useState<string>('');
  const [selectedVersion, setSelectedVersion] = useState<string>('');
  const [selectedDatapack, setSelectedDatapack] = useState<string>('');
  const [selectedDataset, setSelectedDataset] = useState<string>('');
  const [evaluationType, setEvaluationType] = useState<'datapack' | 'dataset'>(
    'datapack'
  );
  const [isEvaluating, setIsEvaluating] = useState(false);
  const [evaluationProgress, setEvaluationProgress] = useState(0);

  const { data: algorithmsData } = useQuery({
    queryKey: ['algorithms'],
    queryFn: () => containerApi.getContainers({ type: 0 }),
  });

  const { data: executionsData } = useQuery({
    queryKey: ['executions'],
    queryFn: () => executionApi.getExecutions({ state: String(2) }),
  });

  const { data: datasetsData } = useQuery({
    queryKey: ['datasets'],
    queryFn: () => datasetApi.getDatasets(),
  });

  const evaluateMutation = useMutation({
    mutationFn: (specs: EvaluateDatapackSpec[] | EvaluateDatasetSpec[]) =>
      evaluationType === 'datapack'
        ? evaluationApi.evaluateDatapacks(specs as EvaluateDatapackSpec[])
        : evaluationApi.evaluateDatasets(specs as EvaluateDatasetSpec[]),
    onSuccess: () => {
      message.success('Evaluation completed successfully!');
      navigate('/evaluations');
    },
    onError: () => {
      message.error('Failed to complete evaluation');
      setIsEvaluating(false);
      setEvaluationProgress(0);
    },
  });

  const handleAlgorithmChange = (algorithmName: string) => {
    setSelectedAlgorithm(algorithmName);
    setSelectedVersion('latest');
    form.setFieldsValue({ algorithm_version: 'latest' });
  };

  const handleEvaluationTypeChange = (type: 'datapack' | 'dataset') => {
    setEvaluationType(type);
    form.setFieldsValue({
      datapack_id: undefined,
      dataset_id: undefined,
      groundtruth_dataset_id: undefined,
    });
    setSelectedDatapack('');
    setSelectedDataset('');
  };

  const handleSubmit = async () => {
    if (!selectedAlgorithm || !selectedVersion) {
      message.error('Please select an algorithm and version');
      return;
    }
    if (evaluationType === 'datapack' && !selectedDatapack) {
      message.error('Please select a datapack');
      return;
    }
    if (evaluationType === 'dataset' && !selectedDataset) {
      message.error('Please select a dataset');
      return;
    }

    const specs: EvaluateDatapackSpec[] = [
      {
        algorithm: { name: selectedAlgorithm, version: selectedVersion },
        datapack: evaluationType === 'datapack' ? selectedDatapack : '',
      },
    ];

    setIsEvaluating(true);
    setEvaluationProgress(0);

    const progressInterval = setInterval(() => {
      setEvaluationProgress((prev) => {
        if (prev >= 90) {
          clearInterval(progressInterval);
          return 90;
        }
        return prev + 10;
      });
    }, 500);

    try {
      await evaluateMutation.mutateAsync(specs);
      setEvaluationProgress(100);
    } finally {
      clearInterval(progressInterval);
      setIsEvaluating(false);
    }
  };

  const handleCancel = () => navigate('/evaluations');

  if (!algorithmsData?.items?.length) {
    return (
      <div style={{ padding: 24 }}>
        <Card>
          <Empty
            description='No algorithms available. Please create an algorithm container first.'
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          >
            <Button type='primary' onClick={() => navigate('/containers/new')}>
              Create Algorithm
            </Button>
          </Empty>
        </Card>
      </div>
    );
  }

  return (
    <div style={{ padding: 24 }}>
      <div style={{ marginBottom: 24 }}>
        <Space>
          <Button icon={<CloseOutlined />} onClick={handleCancel}>
            Back to List
          </Button>
          <Title level={4} style={{ margin: 0 }}>
            New Evaluation
          </Title>
        </Space>
      </div>

      <Row gutter={[24, 24]}>
        <Col xs={24} lg={16}>
          <Card
            title={
              <Space>
                <BarChartOutlined />
                <span>Evaluation Configuration</span>
              </Space>
            }
          >
            <Form
              form={form}
              layout='vertical'
              onFinish={handleSubmit}
              initialValues={{ evaluation_type: 'datapack' }}
            >
              <Alert
                message='Evaluation Setup'
                description='Configure the evaluation by selecting an algorithm, data source, and optional parameters.'
                type='info'
                showIcon
                icon={<InfoCircleOutlined />}
                style={{ marginBottom: 24 }}
              />

              <EvaluationTypeSelect
                evaluationType={evaluationType}
                onChange={handleEvaluationTypeChange}
              />

              <AlgorithmsSection
                algorithmsData={algorithmsData}
                selectedAlgorithm={selectedAlgorithm}
                selectedVersion={selectedVersion}
                onAlgorithmChange={handleAlgorithmChange}
                onVersionChange={setSelectedVersion}
              />

              <DatasetsSection
                evaluationType={evaluationType}
                executionsData={executionsData}
                datasetsData={datasetsData}
                onDatapackChange={setSelectedDatapack}
                onDatasetChange={setSelectedDataset}
              />

              <Form.Item label='Notes' name='notes'>
                <TextArea
                  rows={3}
                  placeholder='Add any notes about this evaluation...'
                />
              </Form.Item>

              {isEvaluating && (
                <Card size='small' style={{ marginBottom: 24 }}>
                  <Space direction='vertical' style={{ width: '100%' }}>
                    <Text strong>Evaluation in progress...</Text>
                    <Progress
                      percent={evaluationProgress}
                      status='active'
                      strokeColor={{
                        '0%': 'var(--color-primary-500)',
                        '100%': 'var(--color-success)',
                      }}
                    />
                  </Space>
                </Card>
              )}

              <Form.Item>
                <Space>
                  <Button
                    type='primary'
                    htmlType='submit'
                    icon={<PlayCircleOutlined />}
                    loading={isEvaluating}
                    disabled={isEvaluating}
                  >
                    Start Evaluation
                  </Button>
                  <Button icon={<CloseOutlined />} onClick={handleCancel}>
                    Cancel
                  </Button>
                </Space>
              </Form.Item>
            </Form>
          </Card>
        </Col>

        <Col xs={24} lg={8}>
          <MetricsSection
            algorithmsCount={algorithmsData?.items?.length || 0}
            datapacksCount={executionsData?.items?.length || 0}
            datasetsCount={datasetsData?.items?.length || 0}
          />
        </Col>
      </Row>
    </div>
  );
};

export default EvaluationForm;
