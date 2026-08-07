import { useCallback, useEffect, useRef, useState } from 'react'

/** Shape shared by every long-running management stream. */
export interface ProgressEvent {
  stage: string
  percentage: number
  message: string
}

export interface StreamedOperation<TRequest> {
  start: (request: TRequest) => Promise<void>
  cancel: () => void
  reset: () => void
  running: boolean
  finished: boolean
  error: string | null
  percentage: number
  stage: string
  events: ProgressEvent[]
}

/**
 * Drives a server-streaming RPC that reports `{stage, percentage, message}`.
 *
 * Provisioning a replica or installing a database takes minutes, so the stream
 * is cancellable and is always aborted on unmount — leaving one running would
 * hold the agent connection open with nothing left to render it.
 */
export function useStreamedOperation<TRequest>(
  invoke: (request: TRequest, options: { signal: AbortSignal }) => AsyncIterable<ProgressEvent>,
): StreamedOperation<TRequest> {
  const [running, setRunning] = useState(false)
  const [finished, setFinished] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [events, setEvents] = useState<ProgressEvent[]>([])

  const controllerRef = useRef<AbortController | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      controllerRef.current?.abort()
    }
  }, [])

  const start = useCallback(
    async (request: TRequest) => {
      controllerRef.current?.abort()
      const controller = new AbortController()
      controllerRef.current = controller

      setRunning(true)
      setFinished(false)
      setError(null)
      setEvents([])

      try {
        for await (const event of invoke(request, { signal: controller.signal })) {
          if (!mountedRef.current || controller.signal.aborted) return
          setEvents((previous) => [...previous, event])
        }
        if (mountedRef.current && !controller.signal.aborted) setFinished(true)
      } catch (err) {
        if (!mountedRef.current || controller.signal.aborted) return
        setError(err instanceof Error ? err.message : 'Operation failed')
      } finally {
        if (mountedRef.current) setRunning(false)
      }
    },
    [invoke],
  )

  const cancel = useCallback(() => {
    controllerRef.current?.abort()
    setRunning(false)
  }, [])

  const reset = useCallback(() => {
    controllerRef.current?.abort()
    setRunning(false)
    setFinished(false)
    setError(null)
    setEvents([])
  }, [])

  const last = events.at(-1)

  return {
    start,
    cancel,
    reset,
    running,
    finished,
    error,
    percentage: last?.percentage ?? 0,
    stage: last?.stage ?? '',
    events,
  }
}
