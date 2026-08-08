import { Skeleton, SimpleGrid, Stack } from '@mantine/core'
import { Suspense, lazy } from 'react'
import { useStatus } from '../hooks/useStatus'
import { AdaptiveStatusCard } from './AdaptiveStatusCard'
import { AdvancedMetrics } from './AdvancedMetrics'
import { ClusterOverview } from './ClusterOverview'
import { PerformanceAdvisor } from './PerformanceAdvisor'
import { StatusPageShell } from './StatusPageShell'
import { StatusStats } from './StatusStats'
import { SystemMetrics } from './SystemMetrics'
import { TopQueries } from './TopQueries'
import { TrafficDistribution } from './TrafficDistribution'

const PerformanceCharts = lazy(() =>
  import('./PerformanceCharts').then((m) => ({ default: m.PerformanceCharts })),
)
const TopologyGraph = lazy(() =>
  import('./TopologyGraph').then((m) => ({ default: m.TopologyGraph })),
)
const SQLPlayground = lazy(() =>
  import('./SQLPlayground').then((m) => ({ default: m.SQLPlayground })),
)

export function DashboardView() {
  const { data } = useStatus()

  return (
    <StatusPageShell
      title="System Dashboard"
      description="Real-time status of your database proxy infrastructure"
    >
      <Stack gap="lg">
        <StatusStats data={data} />

        <Suspense fallback={<Skeleton height={300} radius="md" />}>
          <PerformanceCharts />
        </Suspense>

        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
          <Suspense fallback={<Skeleton height={400} radius="md" />}>
            <TopologyGraph topology={data?.topology} />
          </Suspense>
          <Suspense fallback={<Skeleton height={400} radius="md" />}>
            <SQLPlayground />
          </Suspense>
        </SimpleGrid>

        <TrafficDistribution
          backends={data?.backends ?? []}
          balancerType={data?.balancerType}
          localZone={data?.localZone}
        />

        <ClusterOverview
          backends={data?.backends ?? []}
          protocol={data?.protocol ?? 'Unknown'}
          balancerType={data?.balancerType}
        />

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <AdaptiveStatusCard status={data?.adaptiveStatus} />
          <PerformanceAdvisor suggestions={data?.performanceSuggestions ?? []} />
        </SimpleGrid>

        <SystemMetrics metrics={data?.systemMetrics} />
        <AdvancedMetrics metrics={data?.systemMetrics} />

        <TopQueries queries={data?.topQueries ?? []} />
      </Stack>
    </StatusPageShell>
  )
}
