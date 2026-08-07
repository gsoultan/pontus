import { useForm } from '@tanstack/react-form'
import {
  Alert,
  Button,
  Divider,
  Group,
  Modal,
  PasswordInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'
import { StreamProgressPanel } from '../../common/components/StreamProgressPanel'
import { useProvisionReplica, type ProvisionReplicaInput } from '../hooks/useProvisionReplica'

interface ProvisionReplicaModalProps {
  opened: boolean
  onClose: () => void
  sourceOptions: string[]
  defaultSource?: string
}

const required = (label: string) => ({
  onChange: ({ value }: { value: string }) => (value.trim() ? undefined : `${label} is required`),
})

export function ProvisionReplicaModal({
  opened,
  onClose,
  sourceOptions,
  defaultSource,
}: ProvisionReplicaModalProps) {
  const { start, cancel, reset, running, finished, error, events, percentage, stage } =
    useProvisionReplica()

  const form = useForm({
    defaultValues: {
      sourceAddress: defaultSource ?? sourceOptions[0] ?? '',
      targetAddress: '',
      replicationUser: 'replicator',
      replicationPassword: '',
      sourceAgentToken: '',
      targetAgentToken: '',
      dataDirectory: '/var/lib/postgresql/data',
    } satisfies ProvisionReplicaInput,
    onSubmit: async ({ value }) => {
      await start(value)
    },
  })

  const handleClose = () => {
    if (running) return
    reset()
    form.reset()
    onClose()
  }

  return (
    <Modal
      opened={opened}
      onClose={handleClose}
      title="Provision replica"
      size="lg"
      closeOnClickOutside={!running}
      closeOnEscape={!running}
      withCloseButton={!running}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault()
          event.stopPropagation()
          void form.handleSubmit()
        }}
      >
        <Stack gap="md">
          <Alert color="pontusBlue" variant="light" icon={<IconInfoCircle size={16} />} radius="md">
            Takes a base backup from the source and streams it onto the target host. The target
            must already be reachable by a Pontus agent and have an empty data directory.
          </Alert>

          <form.Field name="sourceAddress" validators={required('Source')}>
            {(field) => (
              <Select
                label="Source node"
                description="The node the base backup is taken from"
                data={sourceOptions}
                value={field.state.value}
                onChange={(value) => field.handleChange(value ?? '')}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                searchable
                disabled={running}
              />
            )}
          </form.Field>

          <form.Field name="targetAddress" validators={required('Target')}>
            {(field) => (
              <TextInput
                label="Target host"
                description="host:port of the new replica"
                placeholder="10.0.0.12:5432"
                value={field.state.value}
                onChange={(event) => field.handleChange(event.currentTarget.value)}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                disabled={running}
              />
            )}
          </form.Field>

          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            <form.Field name="replicationUser" validators={required('Replication user')}>
              {(field) => (
                <TextInput
                  label="Replication user"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.currentTarget.value)}
                  onBlur={field.handleBlur}
                  error={field.state.meta.errors.join(', ') || undefined}
                  disabled={running}
                />
              )}
            </form.Field>

            <form.Field name="replicationPassword" validators={required('Replication password')}>
              {(field) => (
                <PasswordInput
                  label="Replication password"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.currentTarget.value)}
                  onBlur={field.handleBlur}
                  error={field.state.meta.errors.join(', ') || undefined}
                  disabled={running}
                />
              )}
            </form.Field>
          </SimpleGrid>

          <Divider label="Agent credentials" labelPosition="left" />

          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            <form.Field name="sourceAgentToken">
              {(field) => (
                <PasswordInput
                  label="Source agent token"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.currentTarget.value)}
                  disabled={running}
                />
              )}
            </form.Field>

            <form.Field name="targetAgentToken">
              {(field) => (
                <PasswordInput
                  label="Target agent token"
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.currentTarget.value)}
                  disabled={running}
                />
              )}
            </form.Field>
          </SimpleGrid>

          <form.Field name="dataDirectory" validators={required('Data directory')}>
            {(field) => (
              <TextInput
                label="Data directory"
                value={field.state.value}
                onChange={(event) => field.handleChange(event.currentTarget.value)}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                disabled={running}
              />
            )}
          </form.Field>

          <StreamProgressPanel
            events={events}
            percentage={percentage}
            stage={stage}
            running={running}
            finished={finished}
            error={error}
          />

          <Group justify="flex-end" gap="sm">
            {running ? (
              <Button variant="default" color="red" onClick={cancel}>
                Cancel run
              </Button>
            ) : (
              <Button variant="default" onClick={handleClose}>
                {finished ? 'Close' : 'Cancel'}
              </Button>
            )}
            <form.Subscribe selector={(state) => [state.canSubmit, state.isSubmitting] as const}>
              {([canSubmit, isSubmitting]) => (
                <Button type="submit" disabled={!canSubmit || running} loading={isSubmitting}>
                  {finished ? 'Provision again' : 'Start provisioning'}
                </Button>
              )}
            </form.Subscribe>
          </Group>

          {finished && !error && (
            <Text size="sm" c="dimmed">
              The replica is registered. Verify it appears healthy before routing reads to it.
            </Text>
          )}
        </Stack>
      </form>
    </Modal>
  )
}
