import { createLazyFileRoute } from '@tanstack/react-router'
import { StatusContainer } from '../status/components/StatusContainer'

export const Route = createLazyFileRoute('/')({
  component: Index,
})

function Index() {
  return <StatusContainer view="dashboard" />
}
