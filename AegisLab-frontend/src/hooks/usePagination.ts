import { useCallback, useState } from 'react';

interface PaginationState {
  current: number;
  pageSize: number;
}

interface UsePaginationOptions {
  defaultPageSize?: number;
}

export function usePagination(options?: UsePaginationOptions) {
  const { defaultPageSize = 20 } = options ?? {};
  const [pagination, setPagination] = useState<PaginationState>({
    current: 1,
    pageSize: defaultPageSize,
  });

  const onChange = useCallback((page: number, pageSize: number) => {
    setPagination({ current: page, pageSize });
  }, []);

  const reset = useCallback(() => {
    setPagination((prev) => ({ ...prev, current: 1 }));
  }, []);

  return {
    current: pagination.current,
    pageSize: pagination.pageSize,
    onChange,
    reset,
  };
}
