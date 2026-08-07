import { createLazyFileRoute } from '@tanstack/react-router'
import { Container } from '@mantine/core'
import { PageHeader } from '../layout/components/PageHeader'
import { StreamsContainer } from '../streams/components/StreamsContainer'

export const Route = createLazyFileRoute('/streams')({
  component: StreamsPage,
})

function StreamsPage() {
  return (
    <Container size="xl" py="md">
      <PageHeader
        title="Replication Streams"
        description="CDC consumers attached to this proxy — pinned to one node, not load balanced"
      />
      <StreamsContainer />
    </Container>
  )
}
