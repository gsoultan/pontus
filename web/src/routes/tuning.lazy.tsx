import { createLazyFileRoute } from '@tanstack/react-router'
import { TuningView } from '../status/components/TuningView'

export const Route = createLazyFileRoute('/tuning')({
  component: TuningView,
})
