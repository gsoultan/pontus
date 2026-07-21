import { createLazyFileRoute } from '@tanstack/react-router'
import { StatusContainer } from '../status/components/StatusContainer'

export const Route = createLazyFileRoute('/backends')({
  component: BackendsPage,
})

function BackendsPage() {
  return <StatusContainer view="backends" />
}
