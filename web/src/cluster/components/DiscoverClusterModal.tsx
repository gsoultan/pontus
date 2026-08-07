import { useForm } from '@tanstack/react-form'
import { Alert, Button, Group, List, Modal, Select, Stack, Text, ThemeIcon } from '@mantine/core'
import { IconCircleCheck, IconInfoCircle, IconTopologyStar3 } from '@tabler/icons-react'
import { useDiscoverCluster } from '../hooks/useDiscoverCluster'

interface DiscoverClusterModalProps {
  opened: boolean
  onClose: () => void
  primaryOptions: string[]
  defaultPrimary?: string
}

/**
 * Reads a primary's replication topology and registers everything it finds,
 * so an existing cluster doesn't have to be entered node by node.
 */
export function DiscoverClusterModal({
  opened,
  onClose,
  primaryOptions,
  defaultPrimary,
}: DiscoverClusterModalProps) {
  const { discoverCluster, isDiscovering, result, reset } = useDiscoverCluster()

  const form = useForm({
    defaultValues: { primaryAddress: defaultPrimary ?? primaryOptions[0] ?? '' },
    onSubmit: async ({ value }) => {
      await discoverCluster(value)
    },
  })

  const handleClose = () => {
    reset()
    form.reset()
    onClose()
  }

  return (
    <Modal opened={opened} onClose={handleClose} title="Discover cluster nodes" size="md">
      <form
        onSubmit={(event) => {
          event.preventDefault()
          event.stopPropagation()
          void form.handleSubmit()
        }}
      >
        <Stack gap="md">
          <Alert color="pontusBlue" variant="light" icon={<IconInfoCircle size={16} />} radius="md">
            Queries the selected primary for its connected replicas and registers any that Pontus
            does not already know about.
          </Alert>

          <form.Field
            name="primaryAddress"
            validators={{
              onChange: ({ value }) => (value.trim() ? undefined : 'Select a primary'),
            }}
          >
            {(field) => (
              <Select
                label="Primary node"
                data={primaryOptions}
                value={field.state.value}
                onChange={(value) => field.handleChange(value ?? '')}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                searchable
                disabled={isDiscovering}
              />
            )}
          </form.Field>

          {result && (
            <Stack gap="xs">
              <Group gap="xs">
                <ThemeIcon
                  variant="light"
                  color={result.addedNodes.length > 0 ? 'successGreen' : 'gray'}
                  radius="md"
                >
                  <IconCircleCheck size={16} />
                </ThemeIcon>
                <Text fw={700} size="sm">
                  {result.discoveredNodes.length} discovered · {result.addedNodes.length} added
                </Text>
              </Group>

              {result.discoveredNodes.length === 0 ? (
                <Text size="sm" c="dimmed">
                  No replicas are currently streaming from this primary.
                </Text>
              ) : (
                <List size="sm" spacing={2} icon={<IconTopologyStar3 size={14} />}>
                  {result.discoveredNodes.map((node) => (
                    <List.Item key={node}>
                      {node}
                      {result.addedNodes.includes(node) ? (
                        <Text span size="xs" c="successGreen" fw={700}>
                          {' '}
                          — added
                        </Text>
                      ) : (
                        <Text span size="xs" c="dimmed">
                          {' '}
                          — already registered
                        </Text>
                      )}
                    </List.Item>
                  ))}
                </List>
              )}
            </Stack>
          )}

          <Group justify="flex-end" gap="sm">
            <Button variant="default" onClick={handleClose} disabled={isDiscovering}>
              {result ? 'Close' : 'Cancel'}
            </Button>
            <form.Subscribe selector={(state) => state.canSubmit}>
              {(canSubmit) => (
                <Button type="submit" disabled={!canSubmit} loading={isDiscovering}>
                  {result ? 'Scan again' : 'Discover'}
                </Button>
              )}
            </form.Subscribe>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
