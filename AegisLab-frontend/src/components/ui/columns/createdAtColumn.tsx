import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';

export function createdAtColumn<
  T extends { created_at?: string },
>(): ColumnsType<T>[number] {
  return {
    title: 'Created At',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 180,
    render: (val: string) =>
      val ? dayjs(val).format('YYYY-MM-DD HH:mm:ss') : '-',
    sorter: (a: T, b: T) => {
      const aTime = a.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b.created_at ? new Date(b.created_at).getTime() : 0;
      return aTime - bTime;
    },
  };
}
