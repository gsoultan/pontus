import { useForm } from '@tanstack/react-form'
import {
  Alert,
  Button,
  Group,
  Modal,
  PasswordInput,
  Progress,
  Select,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconInfoCircle } from '@tabler/icons-react'
import { useCreateUser } from '../hooks/useCreateUser'

interface CreateUserModalProps {
  opened: boolean
  onClose: () => void
}

const ROLES = [
  { value: 'admin', label: 'Administrator — full control' },
  { value: 'viewer', label: 'Viewer — read-only dashboards' },
]

const MIN_PASSWORD = 12

function strengthOf(password: string): number {
  let score = 0
  if (password.length >= MIN_PASSWORD) score += 40
  if (password.length >= 20) score += 15
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score += 15
  if (/\d/.test(password)) score += 15
  if (/[^\w\s]/.test(password)) score += 15
  return Math.min(100, score)
}

export function CreateUserModal({ opened, onClose }: CreateUserModalProps) {
  const { createUser, isCreating, reset } = useCreateUser()

  const form = useForm({
    defaultValues: { username: '', password: '', confirmPassword: '', role: 'viewer' },
    onSubmit: async ({ value }) => {
      await createUser({
        username: value.username.trim(),
        password: value.password,
        role: value.role,
      })
      form.reset()
      onClose()
    },
  })

  const handleClose = () => {
    reset()
    form.reset()
    onClose()
  }

  return (
    <Modal opened={opened} onClose={handleClose} title="Create management user" size="md">
      <form
        onSubmit={(event) => {
          event.preventDefault()
          event.stopPropagation()
          void form.handleSubmit()
        }}
      >
        <Stack gap="md">
          <Alert color="pontusBlue" variant="light" icon={<IconInfoCircle size={16} />} radius="md">
            Administrators can change cluster configuration and perform failover. Grant the viewer
            role unless full control is needed.
          </Alert>

          <form.Field
            name="username"
            validators={{
              onBlur: ({ value }) =>
                value.trim().length >= 3 ? undefined : 'At least 3 characters',
            }}
          >
            {(field) => (
              <TextInput
                label="Username"
                placeholder="operator"
                autoComplete="off"
                value={field.state.value}
                onChange={(event) => field.handleChange(event.currentTarget.value)}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                disabled={isCreating}
              />
            )}
          </form.Field>

          <form.Field
            name="password"
            validators={{
              onChange: ({ value }) =>
                value.length >= MIN_PASSWORD
                  ? undefined
                  : `At least ${MIN_PASSWORD} characters`,
            }}
          >
            {(field) => {
              const strength = strengthOf(field.state.value)
              return (
                <Stack gap={4}>
                  <PasswordInput
                    label="Password"
                    autoComplete="new-password"
                    value={field.state.value}
                    onChange={(event) => field.handleChange(event.currentTarget.value)}
                    onBlur={field.handleBlur}
                    error={field.state.meta.errors.join(', ') || undefined}
                    disabled={isCreating}
                  />
                  {field.state.value.length > 0 && (
                    <Progress
                      value={strength}
                      size="xs"
                      radius="xl"
                      color={strength < 55 ? 'red' : strength < 85 ? 'orange' : 'successGreen'}
                    />
                  )}
                </Stack>
              )
            }}
          </form.Field>

          <form.Field
            name="confirmPassword"
            validators={{
              onChangeListenTo: ['password'],
              onChange: ({ value, fieldApi }) =>
                value === fieldApi.form.getFieldValue('password')
                  ? undefined
                  : 'Passwords do not match',
            }}
          >
            {(field) => (
              <PasswordInput
                label="Confirm password"
                autoComplete="new-password"
                value={field.state.value}
                onChange={(event) => field.handleChange(event.currentTarget.value)}
                onBlur={field.handleBlur}
                error={field.state.meta.errors.join(', ') || undefined}
                disabled={isCreating}
              />
            )}
          </form.Field>

          <form.Field name="role">
            {(field) => (
              <Select
                label="Role"
                data={ROLES}
                value={field.state.value}
                onChange={(value) => field.handleChange(value ?? 'viewer')}
                allowDeselect={false}
                disabled={isCreating}
              />
            )}
          </form.Field>

          <Text size="xs" c="dimmed">
            Pontus does not expose a user listing API, so this password cannot be recovered later —
            record it now.
          </Text>

          <Group justify="flex-end" gap="sm">
            <Button variant="default" onClick={handleClose} disabled={isCreating}>
              Cancel
            </Button>
            <form.Subscribe selector={(state) => state.canSubmit}>
              {(canSubmit) => (
                <Button type="submit" disabled={!canSubmit} loading={isCreating}>
                  Create user
                </Button>
              )}
            </form.Subscribe>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
