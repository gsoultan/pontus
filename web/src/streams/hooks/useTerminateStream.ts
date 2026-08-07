import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { statusClient } from '../../status/services/statusService'
import { useProjectStore } from '../../store/useProjectStore'

/**
 * Disconnects a replication consumer.
 *
 * Pontus cannot resume a stream on the consumer's behalf — it does not know
 * where the consumer had committed to. Terminating means the consumer restarts
 * from its own checkpoint, so the caller must gate this behind a confirmation.
 */
export function useTerminateStream() {
  const queryClient = useQueryClient()
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  const mutation = useMutation({
    mutationFn: async ({ streamId }: { streamId: string }) => {
      if (!selectedProjectId) throw new Error('No project selected')
      const response = await statusClient.terminateReplicationStream({
        projectId: selectedProjectId,
        proxyId: selectedProxyId || undefined,
        streamId,
      })
      if (!response.success) throw new Error(response.message || 'Termination rejected')
      return response
    },
    onSuccess: (response) => {
      queryClient.invalidateQueries({
        queryKey: ['replication-streams', selectedProjectId, selectedProxyId],
      })
      notifications.show({
        title: 'Stream terminated',
        message: response.message || 'The consumer must resync from its own checkpoint',
        color: 'orange',
      })
    },
    onError: (error: Error) => {
      notifications.show({ title: 'Could not terminate stream', message: error.message, color: 'red' })
    },
  })

  return { terminateStream: mutation.mutateAsync, isTerminating: mutation.isPending }
}
