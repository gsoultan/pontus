import { Box, Button, Center, Container, Stack, Text, ThemeIcon } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import type { ReactNode } from 'react'
import { PageHeader } from '../../layout/components/PageHeader'
import { StatusBreadcrumbs } from './StatusBreadcrumbs'
import { StatusHeaderActions } from './StatusHeaderActions'
import { StatusLoading } from './StatusLoading'
import { useStatus } from '../hooks/useStatus'
import { useStatusContext } from '../hooks/useStatusContext'

interface StatusPageShellProps {
  title: string
  description: string
  children: ReactNode
  /** Appends the active proxy name to the title, e.g. "Backends: analytics". */
  scopedTitle?: boolean
}

/**
 * Common chrome for every status-backed page: breadcrumbs, header, and the
 * loading and error states. Each view owns its own body — this replaces the
 * single container that used to switch on a `view` prop.
 */
export function StatusPageShell({
  title,
  description,
  children,
  scopedTitle = true,
}: StatusPageShellProps) {
  const { data, isLoading, error } = useStatus()
  const { project, proxy } = useStatusContext()

  if (isLoading) return <StatusLoading />

  if (error) {
    return (
      <Center h={400}>
        <Stack align="center">
          <ThemeIcon size={64} radius="xl" color="red" variant="light">
            <IconAlertCircle size={36} />
          </ThemeIcon>
          <Text fw={800} size="xl">
            System Unavailable
          </Text>
          <Text c="dimmed" ta="center" style={{ maxWidth: 400 }}>
            {error.message}
          </Text>
          <Button variant="light" color="red" onClick={() => window.location.reload()} mt="md">
            Retry Connection
          </Button>
        </Stack>
      </Center>
    )
  }

  return (
    <Container size="xl" py="md">
      <Box mb="md">
        <StatusBreadcrumbs projectName={project?.name} proxyName={proxy?.name} />
        <PageHeader
          title={scopedTitle && proxy ? `${title}: ${proxy.name}` : title}
          description={description}
          actions={
            <StatusHeaderActions
              inFailover={data?.inFailover ?? false}
              leaderId={data?.leaderId ?? 'None'}
              protocol={data?.protocol ?? 'Unknown'}
            />
          }
        />
      </Box>
      {children}
    </Container>
  )
}
