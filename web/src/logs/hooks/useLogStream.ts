import { useCallback, useEffect, useRef, useState } from 'react'
import { statusClient } from '../../status/services/statusService'
import { useLogWorker } from './useLogWorker'
import type { LogPage, LogRecord } from '../../workers/logs.worker'

/** How often pending lines are flushed to the worker and the view re-queried. */
const TICK_MS = 300

/** Upper bound on rows handed to React. The worker keeps the rest. */
const VISIBLE_LIMIT = 500

const EMPTY_PAGE: LogPage = { records: [], matched: 0, buffered: 0, dropped: 0 }

export interface UseLogStreamOptions {
  minLevel: string
  search: string
  paused: boolean
}

export interface LogStream {
  page: LogPage
  connected: boolean
  error: string | null
  clear: () => void
}

/**
 * Consumes the server-side log stream into an off-thread ring buffer.
 *
 * Two invariants matter here: the stream is aborted when the component
 * unmounts or the level changes (an orphaned stream keeps the connection and
 * the goroutine behind it alive), and lines are batched rather than posted
 * per-message so a chatty proxy cannot saturate the main thread.
 */
export function useLogStream({ minLevel, search, paused }: UseLogStreamOptions): LogStream {
  const worker = useLogWorker()
  const [page, setPage] = useState<LogPage>(EMPTY_PAGE)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const pending = useRef<Omit<LogRecord, 'seq'>[]>([])
  const pausedRef = useRef(paused)
  pausedRef.current = paused

  // Stream lifecycle: one connection per level, aborted on change/unmount.
  useEffect(() => {
    if (!worker) return
    const controller = new AbortController()
    let cancelled = false

    const consume = async () => {
      try {
        setError(null)
        setConnected(true)
        for await (const entry of statusClient.streamLogs(
          { minLevel },
          { signal: controller.signal },
        )) {
          if (cancelled) break
          if (pausedRef.current) continue
          pending.current.push({
            timestampMs: entry.timestamp ? Number(entry.timestamp.seconds) * 1000 : Date.now(),
            level: entry.level || 'INFO',
            message: entry.message,
            attributes: entry.attributes ?? {},
          })
        }
      } catch (err) {
        if (cancelled || controller.signal.aborted) return
        setError(err instanceof Error ? err.message : 'Log stream disconnected')
      } finally {
        if (!cancelled) setConnected(false)
      }
    }

    void consume()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [worker, minLevel])

  // Flush + re-query on a fixed tick so render cost is bounded regardless of
  // how fast the proxy is logging.
  useEffect(() => {
    if (!worker) return
    let disposed = false

    const tick = async () => {
      if (disposed) return
      const batch = pending.current
      if (batch.length > 0) {
        pending.current = []
        await worker.append(batch)
      }
      const next = await worker.query({ level: minLevel, search }, VISIBLE_LIMIT)
      if (!disposed) setPage(next)
    }

    const id = setInterval(() => void tick(), TICK_MS)
    void tick()

    return () => {
      disposed = true
      clearInterval(id)
    }
  }, [worker, minLevel, search])

  const clear = useCallback(() => {
    pending.current = []
    void worker?.clear().then(() => setPage(EMPTY_PAGE))
  }, [worker])

  return { page, connected, error, clear }
}
