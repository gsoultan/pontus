import { Skeleton, Stack } from '@mantine/core'
import { Suspense, lazy } from 'react'
import { useStatus } from '../hooks/useStatus'
import { StatusPageShell } from './StatusPageShell'
import { TopQueries } from './TopQueries'

const HistoricalTopQueries = lazy(() =>
  import('./HistoricalTopQueries').then((m) => ({ default: m.HistoricalTopQueries })),
)

export function QueriesView() {
  const { data } = useStatus()

  return (
    <StatusPageShell
      title="Query Analysis"
      description="The most frequent and most expensive statements passing through the proxy"
    >
      <Stack gap="lg">
        <TopQueries queries={data?.topQueries ?? []} />
        <Suspense fallback={<Skeleton height={400} radius="md" />}>
          <HistoricalTopQueries />
        </Suspense>
      </Stack>
    </StatusPageShell>
  )
}
