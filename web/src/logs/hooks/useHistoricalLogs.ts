import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import { statusClient } from '../../status/services/statusService'

export interface HistoricalLogQuery {
  rangeHours: number
  level: string
  search: string
  page: number
  pageSize: number
}

/**
 * Historical logs are server-paginated — the store holds seven days of
 * retention, which is far more than the browser should ever hold at once.
 */
export function useHistoricalLogs({
  rangeHours,
  level,
  search,
  page,
  pageSize,
}: HistoricalLogQuery) {
  return useQuery({
    queryKey: ['logs-history', rangeHours, level, search, page, pageSize],
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const now = new Date()
      const startTime = new Date(now.getTime() - rangeHours * 60 * 60 * 1000)
      return await statusClient.getLogs({
        startTime: timestampFromDate(startTime),
        endTime: timestampFromDate(now),
        level: level === 'ALL' ? '' : level,
        search,
        limit: pageSize,
        offset: page * pageSize,
      })
    },
  })
}
