import { useCallback } from 'react'
import { statusClient } from '../../status/services/statusService'
import { useStreamedOperation, type ProgressEvent } from '../../common/hooks/useStreamedOperation'

export interface InstallNodeInput {
  hostAddress: string
  version: string
  targetDirectory: string
  agentToken: string
}

export interface InitializeNodeInput {
  hostAddress: string
  version: string
  dataDirectory: string
  agentToken: string
}

/** Installs the database packages on a clean host via its agent. */
export function useInstallNode() {
  const invoke = useCallback(
    (input: InstallNodeInput, options: { signal: AbortSignal }): AsyncIterable<ProgressEvent> =>
      statusClient.installNode(input, options),
    [],
  )
  return useStreamedOperation(invoke)
}

/** Creates and starts a database cluster on a host that already has packages. */
export function useInitializeNode() {
  const invoke = useCallback(
    (input: InitializeNodeInput, options: { signal: AbortSignal }): AsyncIterable<ProgressEvent> =>
      statusClient.initializeNode(input, options),
    [],
  )
  return useStreamedOperation(invoke)
}
