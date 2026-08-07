import { createLazyFileRoute } from '@tanstack/react-router'
import { DashboardView } from '../status/components/DashboardView'

export const Route = createLazyFileRoute('/')({
  component: DashboardView,
})
