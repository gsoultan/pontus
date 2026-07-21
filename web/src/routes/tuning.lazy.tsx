import { createLazyFileRoute } from '@tanstack/react-router'
import { StatusContainer } from '../status/components/StatusContainer'

export const Route = createLazyFileRoute('/tuning')({
  component: TuningPage,
})

function TuningPage() {
  return <StatusContainer view="tuning" />
}
