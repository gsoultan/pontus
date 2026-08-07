import { useQuery } from '@tanstack/react-query'
import { statusClient } from '../../status/services/statusService'
import { useProjectStore } from '../../store/useProjectStore'

/**
 * Attached CDC/replication consumers.
 *
 * Polled rather than streamed: a stream attaching or detaching is a rare event,
 * and the interesting number — how far behind a consumer is — moves slowly.
 */
export function useReplicationStreams() {
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  return useQuery({
    queryKey: ['replication-streams', selectedProjectId, selectedProxyId],
    enabled: !!selectedProjectId,
    refetchInterval: 10_000,
    queryFn: async () =>
      await statusClient.listReplicationStreams({
        projectId: selectedProjectId!,
        proxyId: selectedProxyId || undefined,
      }),
  })
}
