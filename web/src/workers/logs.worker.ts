import * as Comlink from 'comlink'

/**
 * A log line as it crosses the worker boundary. Deliberately plain — protobuf
 * message instances are not structured-cloneable.
 */
export interface LogRecord {
  seq: number
  timestampMs: number
  level: string
  message: string
  attributes: Record<string, string>
}

export interface LogFilter {
  level?: string
  search?: string
}

export interface LogPage {
  records: LogRecord[]
  /** Matches after filtering, which may exceed `records.length`. */
  matched: number
  /** Records currently retained in the ring buffer. */
  buffered: number
  /** Records evicted since the buffer filled — surfaced so the UI can say so. */
  dropped: number
}

const LEVEL_RANK: Record<string, number> = {
  DEBUG: 10,
  INFO: 20,
  WARN: 30,
  WARNING: 30,
  ERROR: 40,
}

/**
 * Owns the live log ring buffer off the main thread.
 *
 * The buffer is bounded: a proxy under load emits logs faster than React can
 * render them, and an unbounded array in component state is an OOM waiting to
 * happen. Eviction is counted rather than silent so the UI can report it.
 */
class LogBuffer {
  #records: LogRecord[] = []
  #capacity: number
  #dropped = 0
  #seq = 0

  constructor(capacity: number) {
    this.#capacity = capacity
  }

  append(batch: Omit<LogRecord, 'seq'>[]): void {
    for (const entry of batch) {
      this.#records.push({ ...entry, seq: this.#seq++ })
    }
    const overflow = this.#records.length - this.#capacity
    if (overflow > 0) {
      this.#records.splice(0, overflow)
      this.#dropped += overflow
    }
  }

  /** Returns the newest `limit` matches, oldest-first, for a stable render. */
  query(filter: LogFilter, limit: number): LogPage {
    const minRank = filter.level ? (LEVEL_RANK[filter.level.toUpperCase()] ?? 0) : 0
    const needle = filter.search?.trim().toLowerCase()

    const matches: LogRecord[] = []
    for (const record of this.#records) {
      if (minRank > 0 && (LEVEL_RANK[record.level.toUpperCase()] ?? 0) < minRank) continue
      if (needle && !this.#matchesText(record, needle)) continue
      matches.push(record)
    }

    return {
      records: matches.length > limit ? matches.slice(-limit) : matches,
      matched: matches.length,
      buffered: this.#records.length,
      dropped: this.#dropped,
    }
  }

  #matchesText(record: LogRecord, needle: string): boolean {
    if (record.message.toLowerCase().includes(needle)) return true
    for (const [key, value] of Object.entries(record.attributes)) {
      if (key.toLowerCase().includes(needle) || value.toLowerCase().includes(needle)) return true
    }
    return false
  }

  clear(): void {
    this.#records = []
    this.#dropped = 0
    this.#seq = 0
  }

  setCapacity(capacity: number): void {
    this.#capacity = Math.max(1, capacity)
    const overflow = this.#records.length - this.#capacity
    if (overflow > 0) {
      this.#records.splice(0, overflow)
      this.#dropped += overflow
    }
  }
}

const api = {
  buffer: new LogBuffer(5000),
  append(batch: Omit<LogRecord, 'seq'>[]) {
    this.buffer.append(batch)
  },
  query(filter: LogFilter, limit: number): LogPage {
    return this.buffer.query(filter, limit)
  },
  clear() {
    this.buffer.clear()
  },
  setCapacity(capacity: number) {
    this.buffer.setCapacity(capacity)
  },
}

export type LogWorkerApi = typeof api

Comlink.expose(api)
