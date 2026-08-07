import { createLazyFileRoute } from '@tanstack/react-router'
import { QueriesView } from '../status/components/QueriesView'

export const Route = createLazyFileRoute('/queries')({
  component: QueriesView,
})
