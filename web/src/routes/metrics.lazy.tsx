import { createLazyFileRoute } from '@tanstack/react-router'
import { MetricsView } from '../status/components/MetricsView'

export const Route = createLazyFileRoute('/metrics')({
  component: MetricsView,
})
