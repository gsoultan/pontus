import { createLazyFileRoute } from '@tanstack/react-router'
import { BackendsView } from '../status/components/BackendsView'

export const Route = createLazyFileRoute('/backends')({
  component: BackendsView,
})
