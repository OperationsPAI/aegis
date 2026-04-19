import { useMutation, useQueryClient } from '@tanstack/react-query';
import { message } from 'antd';

import { usersApi } from '@/api/users';

export function useUserMutations() {
  const queryClient = useQueryClient();

  const assignRoleMutation = useMutation({
    mutationFn: ({ userId, roleId }: { userId: number; roleId: number }) =>
      usersApi.assignRole(userId, roleId),
    onSuccess: () => {
      message.success('Role assigned successfully');
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: () => {
      message.error('Failed to assign role');
    },
  });

  const removeRoleMutation = useMutation({
    mutationFn: ({ userId, roleId }: { userId: number; roleId: number }) =>
      usersApi.removeRole(userId, roleId),
    onSuccess: () => {
      message.success('Role removed successfully');
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: () => {
      message.error('Failed to remove role');
    },
  });

  const deleteUserMutation = useMutation({
    mutationFn: (id: number) => usersApi.deleteUser(id),
    onSuccess: () => {
      message.success('User deleted successfully');
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: () => {
      message.error('Failed to delete user');
    },
  });

  return { assignRoleMutation, removeRoleMutation, deleteUserMutation };
}
