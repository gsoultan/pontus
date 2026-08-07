import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Code,
  Group,
  List,
  Modal,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
} from '@mantine/core'
import { IconAlertTriangle, IconShieldExclamation } from '@tabler/icons-react'
import type { ReactNode } from 'react'

export interface ConfirmDangerModalProps {
  opened: boolean
  onClose: () => void
  onConfirm: () => void | Promise<void>
  title: string
  description: ReactNode
  /** Bullet points spelling out the blast radius. */
  consequences?: string[]
  /**
   * When set, the operator must type this string exactly before the confirm
   * button enables. Reserve it for actions that cannot be undone.
   */
  confirmationText?: string
  confirmLabel: string
  loading?: boolean
  severity?: 'danger' | 'warning'
}

/**
 * The single confirmation surface for destructive operations.
 *
 * Restarting a service and shutting a database down are not the same risk, so
 * this takes the blast radius as data: reversible actions get a plain confirm,
 * irreversible ones require typing the target's name. Nothing in the product
 * should be reaching for `window.confirm` — a native dialog gives the operator
 * no idea which node they are about to take offline.
 */
export function ConfirmDangerModal({
  opened,
  onClose,
  onConfirm,
  title,
  description,
  consequences,
  confirmationText,
  confirmLabel,
  loading = false,
  severity = 'danger',
}: ConfirmDangerModalProps) {
  const [typed, setTyped] = useState('')
  const color = severity === 'danger' ? 'red' : 'orange'
  const armed = !confirmationText || typed.trim() === confirmationText

  // Never carry a previous confirmation across openings — that would let a
  // second, different target inherit the first one's typed approval.
  useEffect(() => {
    if (!opened) setTyped('')
  }, [opened])

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="sm">
          <ThemeIcon variant="light" color={color} radius="md">
            {severity === 'danger' ? (
              <IconShieldExclamation size={18} />
            ) : (
              <IconAlertTriangle size={18} />
            )}
          </ThemeIcon>
          <Text fw={700}>{title}</Text>
        </Group>
      }
      centered
      size="md"
    >
      <Stack gap="md">
        <Text size="sm">{description}</Text>

        {consequences && consequences.length > 0 && (
          <Alert color={color} variant="light" radius="md">
            <List size="sm" spacing={4}>
              {consequences.map((item) => (
                <List.Item key={item}>{item}</List.Item>
              ))}
            </List>
          </Alert>
        )}

        {confirmationText && (
          <Stack gap={6}>
            <Text size="sm">
              Type <Code>{confirmationText}</Code> to confirm.
            </Text>
            <TextInput
              value={typed}
              onChange={(event) => setTyped(event.currentTarget.value)}
              placeholder={confirmationText}
              autoComplete="off"
              spellCheck={false}
              data-autofocus
              aria-label={`Type ${confirmationText} to confirm`}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && armed && !loading) void onConfirm()
              }}
            />
          </Stack>
        )}

        <Group justify="flex-end" gap="sm">
          <Button variant="default" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color={color} onClick={() => void onConfirm()} disabled={!armed} loading={loading}>
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
