import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { statusClient } from '../../status/services/statusService'
import { useProjectStore } from '../../store/useProjectStore'

/**
 * Walks a primary's replication topology and registers every node it finds.
 * Returns both what was discovered and what was actually added, so the UI can
 * distinguish "already known" from "new".
 */
export function useDiscoverCluster() {
  const queryClient = useQueryClient()
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  const mutation = useMutation({
    mutationFn: async ({ primaryAddress }: { primaryAddress: string }) => {
      if (!selectedProjectId || !selectedProxyId) throw new Error('No proxy selected')
      return await statusClient.discoverCluster({
        projectId: selectedProjectId,
        proxyId: selectedProxyId,
        primaryAddress,
      })
    },
    onSuccess: (response) => {
      queryClient.invalidateQueries({ queryKey: ['status', selectedProjectId, selectedProxyId] })
      queryClient.invalidateQueries({ queryKey: ['projects'] })
      notifications.show({
        title: 'Discovery complete',
        message: `${response.discoveredNodes.length} node(s) found, ${response.addedNodes.length} added`,
        color: response.addedNodes.length > 0 ? 'green' : 'blue',
      })
    },
    onError: (error: Error) => {
      notifications.show({ title: 'Discovery failed', message: error.message, color: 'red' })
    },
  })

  return {
    discoverCluster: mutation.mutateAsync,
    isDiscovering: mutation.isPending,
    result: mutation.data,
    reset: mutation.reset,
  }
}
