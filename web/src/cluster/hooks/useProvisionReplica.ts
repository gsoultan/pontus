import { useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { statusClient } from '../../status/services/statusService'
import { useProjectStore } from '../../store/useProjectStore'
import { useStreamedOperation, type ProgressEvent } from '../../common/hooks/useStreamedOperation'

export interface ProvisionReplicaInput {
  sourceAddress: string
  targetAddress: string
  replicationUser: string
  replicationPassword: string
  sourceAgentToken: string
  targetAgentToken: string
  dataDirectory: string
}

/** Streams a base backup from a source node onto a fresh target host. */
export function useProvisionReplica() {
  const queryClient = useQueryClient()
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  const invoke = useCallback(
    (input: ProvisionReplicaInput, options: { signal: AbortSignal }): AsyncIterable<ProgressEvent> => {
      if (!selectedProjectId || !selectedProxyId) throw new Error('No proxy selected')
      return statusClient.provisionReplica(
        { projectId: selectedProjectId, proxyId: selectedProxyId, ...input },
        options,
      )
    },
    [selectedProjectId, selectedProxyId],
  )

  const operation = useStreamedOperation(invoke)

  const start = useCallback(
    async (input: ProvisionReplicaInput) => {
      await operation.start(input)
      queryClient.invalidateQueries({ queryKey: ['status', selectedProjectId, selectedProxyId] })
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
    [operation, queryClient, selectedProjectId, selectedProxyId],
  )

  return { ...operation, start }
}
