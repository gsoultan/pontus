import { createLazyFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import {
  Badge,
  Box,
  Button,
  Container,
  Divider,
  Group,
  NumberInput,
  Paper,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  rem,
} from '@mantine/core'
import {
  IconDeviceFloppy,
  IconRefresh,
  IconSettings,
  IconUserPlus,
  IconUsers,
} from '@tabler/icons-react'
import { useForm } from '@tanstack/react-form'
import { PageHeader } from '../layout/components/PageHeader'
import { useClusterConfig } from '../status/hooks/useClusterConfig'
import { useServerInfo } from '../services/useServerInfo'
import { useAuthStore } from '../store/useAuthStore'
import { CreateUserModal } from '../users/components/CreateUserModal'

export const Route = createLazyFileRoute('/settings')({
  component: SettingsPage,
})

// Must stay in sync with newBalancer() in the registry. "Random" used to be
// offered here and has no implementation — it fell through to round-robin, as
// did least-conns, so the dashboard reported a strategy that was not running.
const BALANCERS = [
  { value: 'round-robin', label: 'Round Robin' },
  { value: 'weighted-round-robin', label: 'Weighted Round Robin' },
  { value: 'least-conns', label: 'Least Connections' },
  { value: 'p2c', label: 'Power of Two Choices' },
  { value: 'peak-ewma', label: 'Peak EWMA (latency-aware)' },
  { value: 'consistent', label: 'Consistent Hash' },
]

const POOLING_MODES = [
  { value: 'transaction', label: 'Transaction' },
  { value: 'session', label: 'Session' },
  { value: 'statement', label: 'Statement' },
]

interface SettingsFormProps {
  initialValues: {
    query_timeout: string
    max_conns: number
    balancer: string
    pooling_mode: string
  }
  isUpdating: boolean
  onSubmit: (parameters: Record<string, string>) => Promise<unknown>
  onSync: () => void
}

/**
 * Split from the page so the form is constructed once the config has loaded.
 * Seeding TanStack Form from `defaultValues` removes the effect that used to
 * copy server state into form state on every render pass.
 */
function SettingsForm({ initialValues, isUpdating, onSubmit, onSync }: SettingsFormProps) {
  const form = useForm({
    defaultValues: initialValues,
    onSubmit: async ({ value }) => {
      await onSubmit({
        query_timeout: value.query_timeout,
        max_conns: String(value.max_conns),
        balancer: value.balancer,
        pooling_mode: value.pooling_mode,
      })
      form.reset(value)
    },
  })

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        event.stopPropagation()
        void form.handleSubmit()
      }}
    >
      <Stack gap="lg">
        <Paper p="md" radius="md">
          <Stack gap="md">
            <Group justify="space-between">
              <Group gap="sm">
                <ThemeIcon size="lg" variant="light" color="pontusBlue">
                  <IconSettings size={20} />
                </ThemeIcon>
                <Box>
                  <Text fw={700} size="md">
                    Runtime Parameters
                  </Text>
                  <Text size="xs" c="dimmed">
                    Applied across every node in the cluster
                  </Text>
                </Box>
              </Group>
              <Group gap="xs">
                <form.Subscribe selector={(state) => state.isDirty}>
                  {(isDirty) =>
                    isDirty ? (
                      <Badge variant="light" color="orange" size="sm">
                        Unsaved
                      </Badge>
                    ) : null
                  }
                </form.Subscribe>
                <Button
                  variant="subtle"
                  color="gray"
                  size="sm"
                  leftSection={<IconRefresh size={16} />}
                  onClick={onSync}
                  disabled={isUpdating}
                >
                  Sync
                </Button>
                <form.Subscribe selector={(state) => [state.canSubmit, state.isDirty] as const}>
                  {([canSubmit, isDirty]) => (
                    <Button
                      type="submit"
                      size="sm"
                      leftSection={<IconDeviceFloppy size={16} />}
                      loading={isUpdating}
                      disabled={!canSubmit || !isDirty}
                    >
                      Save Changes
                    </Button>
                  )}
                </form.Subscribe>
              </Group>
            </Group>

            <Divider />

            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
              <form.Field
                name="query_timeout"
                validators={{
                  onChange: ({ value }) =>
                    /^\d+(ms|s|m|h)$/.test(value.trim())
                      ? undefined
                      : 'Use a Go duration such as 30s or 500ms',
                }}
              >
                {(field) => (
                  <TextInput
                    label="Query Timeout"
                    description="Maximum time for a single query"
                    placeholder="30s"
                    value={field.state.value}
                    onChange={(event) => field.handleChange(event.currentTarget.value)}
                    onBlur={field.handleBlur}
                    error={field.state.meta.errors.join(', ') || undefined}
                  />
                )}
              </form.Field>

              <form.Field
                name="max_conns"
                validators={{
                  onChange: ({ value }) =>
                    value > 0 ? undefined : 'Must be greater than zero',
                }}
              >
                {(field) => (
                  <NumberInput
                    label="Global Connection Limit"
                    description="Concurrent sessions per proxy"
                    placeholder="1000"
                    min={1}
                    value={field.state.value}
                    onChange={(value) => field.handleChange(Number(value) || 0)}
                    onBlur={field.handleBlur}
                    error={field.state.meta.errors.join(', ') || undefined}
                  />
                )}
              </form.Field>

              <form.Field name="balancer">
                {(field) => (
                  <Select
                    label="Balancing Strategy"
                    description="Logic for backend routing"
                    data={BALANCERS}
                    value={field.state.value}
                    onChange={(value) => field.handleChange(value ?? 'round-robin')}
                    allowDeselect={false}
                  />
                )}
              </form.Field>

              <form.Field name="pooling_mode">
                {(field) => (
                  <Select
                    label="Pooling Mode"
                    description="Database link management"
                    data={POOLING_MODES}
                    value={field.state.value}
                    onChange={(value) => field.handleChange(value ?? 'transaction')}
                    allowDeselect={false}
                  />
                )}
              </form.Field>
            </SimpleGrid>
          </Stack>
        </Paper>

      </Stack>
    </form>
  )
}

function SettingsPage() {
  const { config, isLoading, isUpdating, updateConfig, refetch } = useClusterConfig()
  const { data: serverInfo } = useServerInfo()
  const isAdmin = useAuthStore((state) => state.role === 'admin')
  const [userModalOpen, setUserModalOpen] = useState(false)

  return (
    <Container size="xl" py="md">
      <PageHeader
        title="Settings"
        description="Global system configuration and security parameters"
      />

      <Stack gap="lg">
        {isLoading ? (
          <Stack gap="lg">
            <Skeleton height={260} radius="md" />
            <Skeleton height={200} radius="md" />
          </Stack>
        ) : (
          <SettingsForm
            initialValues={{
              query_timeout: config.query_timeout || '30s',
              max_conns: Number.parseInt(config.max_conns || '1000', 10),
              balancer: config.balancer || 'round-robin',
              pooling_mode: config.pooling_mode || 'transaction',
            }}
            isUpdating={isUpdating}
            onSubmit={updateConfig}
            onSync={() => void refetch()}
          />
        )}

        {isAdmin && (
          <Paper p="md" radius="md">
            <Group justify="space-between" wrap="wrap" gap="sm">
              <Group gap="sm">
                <ThemeIcon size="lg" variant="light" color="pontusBlue">
                  <IconUsers size={20} />
                </ThemeIcon>
                <Box>
                  <Text fw={700} size="md">
                    Management Users
                  </Text>
                  <Text size="xs" c="dimmed">
                    Accounts that can sign in to this dashboard
                  </Text>
                </Box>
              </Group>
              <Button
                variant="light"
                leftSection={<IconUserPlus size={16} />}
                onClick={() => setUserModalOpen(true)}
              >
                Create user
              </Button>
            </Group>
          </Paper>
        )}

        <Paper
          p="md"
          radius="md"
          style={{ borderLeft: `${rem(3)} solid var(--mantine-color-pontusBlue-6)` }}
        >
          <Stack gap="xs">
            <Text fw={700} size="sm">
              System Information
            </Text>
            <Group gap="xl">
              <Box>
                <Text size="10px" c="dimmed" fw={600}>
                  Version
                </Text>
                <Text size="xs" fw={600}>
                  {serverInfo?.version || '—'}
                </Text>
              </Box>
              <Box>
                <Text size="10px" c="dimmed" fw={600}>
                  Commit
                </Text>
                <Text size="xs" fw={600} ff="monospace">
                  {serverInfo?.commit ? serverInfo.commit.slice(0, 12) : '—'}
                </Text>
              </Box>
              <Box>
                <Text size="10px" c="dimmed" fw={600}>
                  Built
                </Text>
                <Text size="xs" fw={600}>
                  {serverInfo?.buildTime || '—'}
                </Text>
              </Box>
            </Group>
          </Stack>
        </Paper>
      </Stack>

      <CreateUserModal opened={userModalOpen} onClose={() => setUserModalOpen(false)} />
    </Container>
  )
}
