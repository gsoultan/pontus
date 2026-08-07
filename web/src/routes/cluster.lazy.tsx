import { createLazyFileRoute } from '@tanstack/react-router'
import { Container } from '@mantine/core'
import { PageHeader } from '../layout/components/PageHeader'
import { ClusterOperations } from '../cluster/components/ClusterOperations'

export const Route = createLazyFileRoute('/cluster')({
  component: ClusterPage,
})

function ClusterPage() {
  return (
    <Container size="xl" py="md">
      <PageHeader
        title="Cluster Operations"
        description="Failover, replica provisioning, topology discovery and host bring-up"
      />
      <ClusterOperations />
    </Container>
  )
}
