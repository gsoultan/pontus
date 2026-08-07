import { useMutation } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { statusClient } from '../../status/services/statusService'

export interface CreateUserInput {
  username: string
  password: string
  role: string
}

/**
 * Creates a management user. There is no ListUsers RPC, so this is create-only
 * by design of the contract — the UI must not imply a roster it cannot read.
 */
export function useCreateUser() {
  const mutation = useMutation({
    mutationFn: async (input: CreateUserInput) => await statusClient.createUser(input),
    onSuccess: (response) => {
      notifications.show({
        title: 'User created',
        message: `${response.username} was created with the ${response.role} role`,
        color: 'green',
      })
    },
    onError: (error: Error) => {
      notifications.show({
        title: 'Could not create user',
        message: error.message,
        color: 'red',
      })
    },
  })

  return {
    createUser: mutation.mutateAsync,
    isCreating: mutation.isPending,
    createdUser: mutation.data,
    reset: mutation.reset,
  }
}
