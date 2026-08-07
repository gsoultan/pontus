import { Skeleton } from '@mantine/core'
import { Suspense, lazy } from 'react'
import { StatusPageShell } from './StatusPageShell'

const TuningAdvisor = lazy(() =>
  import('./TuningAdvisor').then((m) => ({ default: m.TuningAdvisor })),
)

export function TuningView() {
  return (
    <StatusPageShell
      title="Tuning Advisor"
      description="Hardware-aware configuration recommendations for your database nodes"
    >
      <Suspense fallback={<Skeleton height={400} radius="md" />}>
        <TuningAdvisor />
      </Suspense>
    </StatusPageShell>
  )
}
