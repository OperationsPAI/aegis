import {
  DeleteOutlined,
  SearchOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { Button, Card, Col, Input, Row, Select, Space } from 'antd';

const { Search } = Input;
const { Option } = Select;

type DatasetType = 'Trace' | 'Log' | 'Metric';

interface DatasetFiltersProps {
  typeFilter: DatasetType | undefined;
  selectedCount: number;
  onSearch: (value: string) => void;
  onTypeFilter: (type: DatasetType | undefined) => void;
  onBatchDelete: () => void;
  onUpload: () => void;
}

const DatasetFilters: React.FC<DatasetFiltersProps> = ({
  typeFilter,
  selectedCount,
  onSearch,
  onTypeFilter,
  onBatchDelete,
  onUpload,
}) => {
  return (
    <Card style={{ marginBottom: 16 }}>
      <Row gutter={[16, 16]} align='middle'>
        <Col xs={24} sm={12} md={8}>
          <Search
            placeholder='Search datasets by name or description...'
            allowClear
            enterButton={<SearchOutlined />}
            onSearch={onSearch}
            style={{ width: '100%' }}
          />
        </Col>
        <Col xs={24} sm={12} md={4}>
          <Select
            placeholder='Filter by type'
            allowClear
            style={{ width: '100%' }}
            onChange={onTypeFilter}
            value={typeFilter}
          >
            <Option value='Trace'>Trace</Option>
            <Option value='Log'>Log</Option>
            <Option value='Metric'>Metric</Option>
          </Select>
        </Col>
        <Col xs={24} sm={24} md={12} style={{ textAlign: 'right' }}>
          <Space>
            {selectedCount > 0 && (
              <Button danger icon={<DeleteOutlined />} onClick={onBatchDelete}>
                Delete Selected ({selectedCount})
              </Button>
            )}
            <Button icon={<UploadOutlined />} onClick={onUpload}>
              Import Dataset
            </Button>
          </Space>
        </Col>
      </Row>
    </Card>
  );
};

export default DatasetFilters;
