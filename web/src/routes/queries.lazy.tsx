import { createLazyFileRoute } from '@tanstack/react-router'
import { StatusContainer } from '../status/components/StatusContainer'

export const Route = createLazyFileRoute('/queries')({
  component: QueriesPage,
})

function QueriesPage() {
  return <StatusContainer view="queries" />
}
