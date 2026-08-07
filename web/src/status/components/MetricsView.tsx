import { Skeleton, Stack } from '@mantine/core'
import { Suspense, lazy } from 'react'
import { useStatus } from '../hooks/useStatus'
import { AdvancedMetrics } from './AdvancedMetrics'
import { StatusPageShell } from './StatusPageShell'
import { StatusStats } from './StatusStats'
import { SystemMetrics } from './SystemMetrics'

const HistoricalPerformanceCharts = lazy(() =>
  import('./HistoricalPerformanceCharts').then((m) => ({ default: m.HistoricalPerformanceCharts })),
)

export function MetricsView() {
  const { data } = useStatus()

  return (
    <StatusPageShell
      title="Performance Metrics"
      description="Detailed throughput, latency and error rate monitoring"
    >
      <Stack gap="lg">
        <StatusStats data={data} />
        <Suspense fallback={<Skeleton height={500} radius="md" />}>
          <HistoricalPerformanceCharts />
        </Suspense>
        <SystemMetrics metrics={data?.systemMetrics} />
        <AdvancedMetrics metrics={data?.systemMetrics} />
      </Stack>
    </StatusPageShell>
  )
}
