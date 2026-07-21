import { createLazyFileRoute } from '@tanstack/react-router'
import { StatusContainer } from '../status/components/StatusContainer'

export const Route = createLazyFileRoute('/metrics')({
  component: MetricsPage,
})

function MetricsPage() {
  return <StatusContainer view="metrics" />
}
