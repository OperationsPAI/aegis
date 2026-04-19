import { useMutation, useQueryClient } from '@tanstack/react-query';
import { message } from 'antd';

import { roleApi } from '@/api/roles';

export function useRoleMutations() {
  const queryClient = useQueryClient();

  const createRoleMutation = useMutation({
    mutationFn: (data: {
      name: string;
      display_name: string;
      description?: string;
    }) => roleApi.createRole(data),
    onSuccess: () => {
      message.success('Role created successfully');
      queryClient.invalidateQueries({ queryKey: ['admin-roles'] });
    },
    onError: () => {
      message.error('Failed to create role');
    },
  });

  const deleteRoleMutation = useMutation({
    mutationFn: (id: number) => roleApi.deleteRole(id),
    onSuccess: () => {
      message.success('Role deleted successfully');
      queryClient.invalidateQueries({ queryKey: ['admin-roles'] });
    },
    onError: () => {
      message.error('Failed to delete role');
    },
  });

  return { createRoleMutation, deleteRoleMutation };
}
