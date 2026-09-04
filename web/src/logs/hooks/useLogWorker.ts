import { useEffect, useRef, useState } from 'react'
import * as Comlink from 'comlink'
import type { LogWorkerApi } from '../../workers/logs.worker'

/**
 * Spawns the log ring-buffer worker and tears it down with the component.
 * Returns null until the worker is live.
 */
export function useLogWorker(): Comlink.Remote<LogWorkerApi> | null {
  const [api, setApi] = useState<Comlink.Remote<LogWorkerApi> | null>(null)
  const workerRef = useRef<Worker | null>(null)

  // Synchronising with an external system, which is what effects are for and
  // what this rule explicitly allows: a Worker cannot be constructed during
  // render, it needs teardown, and consumers have to re-render once its proxy
  // exists. There is nothing to derive.
  useEffect(() => {
    const worker = new Worker(new URL('../../workers/logs.worker.ts', import.meta.url), {
      type: 'module',
      name: 'pontus-logs',
    })
    workerRef.current = worker
    // oxlint-disable-next-line react/set-state-in-effect
    setApi(() => Comlink.wrap<LogWorkerApi>(worker))

    return () => {
      worker.terminate()
      workerRef.current = null
      setApi(null)
    }
  }, [])

  return api
}
