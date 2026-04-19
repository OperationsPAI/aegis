import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { Button, Input, Space } from 'antd';

/** Tiny key-value pair editor for fault params. */
const ParamEditor: React.FC<{
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}> = ({ value, onChange }) => {
  const entries = Object.entries(value);

  const handleAdd = () => {
    onChange({ ...value, '': '' });
  };

  const handleRemove = (key: string) => {
    const next = { ...value };
    delete next[key];
    onChange(next);
  };

  const handleChange = (
    oldKey: string,
    field: 'key' | 'value',
    newVal: string
  ) => {
    const next: Record<string, string> = {};
    for (const [k, v] of Object.entries(value)) {
      if (k === oldKey) {
        if (field === 'key') next[newVal] = v;
        else next[k] = newVal;
      } else {
        next[k] = v;
      }
    }
    onChange(next);
  };

  return (
    <div>
      {entries.map(([k, v], idx) => (
        <Space key={idx} style={{ display: 'flex', marginBottom: 4 }}>
          <Input
            placeholder='key'
            value={k}
            onChange={(e) => handleChange(k, 'key', e.target.value)}
            style={{ width: 140 }}
          />
          <Input
            placeholder='value'
            value={v}
            onChange={(e) => handleChange(k, 'value', e.target.value)}
            style={{ width: 200 }}
          />
          <Button
            type='text'
            danger
            icon={<DeleteOutlined />}
            onClick={() => handleRemove(k)}
          />
        </Space>
      ))}
      <Button
        type='dashed'
        size='small'
        icon={<PlusOutlined />}
        onClick={handleAdd}
      >
        Add parameter
      </Button>
    </div>
  );
};

export default ParamEditor;
