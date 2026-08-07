import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { statusClient } from '../../status/services/statusService'
import { useProjectStore } from '../../store/useProjectStore'

/**
 * Promotes a replica to primary. This is the manual failover path — it changes
 * which node accepts writes, so the caller is expected to gate it behind a
 * typed confirmation.
 */
export function usePromoteBackend() {
  const queryClient = useQueryClient()
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  const mutation = useMutation({
    mutationFn: async ({ address }: { address: string }) => {
      if (!selectedProjectId || !selectedProxyId) throw new Error('No proxy selected')
      const response = await statusClient.promoteBackend({
        projectId: selectedProjectId,
        proxyId: selectedProxyId,
        address,
      })
      if (!response.success) throw new Error(response.message || 'Promotion rejected')
      return response
    },
    onSuccess: (response, variables) => {
      queryClient.invalidateQueries({ queryKey: ['status', selectedProjectId, selectedProxyId] })
      queryClient.invalidateQueries({ queryKey: ['projects'] })
      notifications.show({
        title: 'Promotion complete',
        message: response.message || `${variables.address} is now the primary`,
        color: 'green',
      })
    },
    onError: (error: Error) => {
      notifications.show({
        title: 'Promotion failed',
        message: error.message,
        color: 'red',
      })
    },
  })

  return {
    promoteBackend: mutation.mutateAsync,
    isPromoting: mutation.isPending,
  }
}
