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

  useEffect(() => {
    const worker = new Worker(new URL('../../workers/logs.worker.ts', import.meta.url), {
      type: 'module',
      name: 'pontus-logs',
    })
    workerRef.current = worker
    setApi(() => Comlink.wrap<LogWorkerApi>(worker))

    return () => {
      worker.terminate()
      workerRef.current = null
      setApi(null)
    }
  }, [])

  return api
}
