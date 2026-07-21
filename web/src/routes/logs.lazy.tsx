import { createLazyFileRoute } from '@tanstack/react-router'
import { LogsContainer } from '../logs/components/LogsContainer'

export const Route = createLazyFileRoute('/logs')({
  component: Logs,
})

function Logs() {
  return <LogsContainer />
}
