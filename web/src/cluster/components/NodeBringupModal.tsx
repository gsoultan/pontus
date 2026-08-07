import { useState } from 'react'
import { useForm } from '@tanstack/react-form'
import {
  Alert,
  Button,
  Group,
  Modal,
  PasswordInput,
  Select,
  Stack,
  Tabs,
  TextInput,
} from '@mantine/core'
import { IconDownload, IconInfoCircle, IconPlayerPlay } from '@tabler/icons-react'
import { StreamProgressPanel } from '../../common/components/StreamProgressPanel'
import { useAvailableVersions } from '../hooks/useAvailableVersions'
import { useInitializeNode, useInstallNode } from '../hooks/useNodeBringup'

interface NodeBringupModalProps {
  opened: boolean
  onClose: () => void
}

const required = (label: string) => ({
  onChange: ({ value }: { value: string }) => (value.trim() ? undefined : `${label} is required`),
})

/**
 * Brings a bare host into service: install the database packages, then create
 * and start the cluster. Both steps run through the host's Pontus agent.
 */
export function NodeBringupModal({ opened, onClose }: NodeBringupModalProps) {
  const [tab, setTab] = useState<string | null>('install')
  const { data: versions = [] } = useAvailableVersions()
  const install = useInstallNode()
  const initialize = useInitializeNode()

  const versionData = versions.length > 0 ? versions : ['17', '16', '15']
  const busy = install.running || initialize.running

  const installForm = useForm({
    defaultValues: {
      hostAddress: '',
      version: versionData[0] ?? '17',
      targetDirectory: '/usr/lib/postgresql',
      agentToken: '',
    },
    onSubmit: async ({ value }) => {
      await install.start(value)
    },
  })

  const initForm = useForm({
    defaultValues: {
      hostAddress: '',
      version: versionData[0] ?? '17',
      dataDirectory: '/var/lib/postgresql/data',
      agentToken: '',
    },
    onSubmit: async ({ value }) => {
      await initialize.start(value)
    },
  })

  const handleClose = () => {
    if (busy) return
    install.reset()
    initialize.reset()
    onClose()
  }

  return (
    <Modal
      opened={opened}
      onClose={handleClose}
      title="Bring up a database host"
      size="lg"
      closeOnClickOutside={!busy}
      closeOnEscape={!busy}
      withCloseButton={!busy}
    >
      <Stack gap="md">
        <Alert color="pontusBlue" variant="light" icon={<IconInfoCircle size={16} />} radius="md">
          The target host must already be running a Pontus agent. Install the packages first, then
          initialize the cluster.
        </Alert>

        <Tabs value={tab} onChange={setTab} variant="outline" radius="md">
          <Tabs.List>
            <Tabs.Tab value="install" leftSection={<IconDownload size={16} />} disabled={busy}>
              1 · Install packages
            </Tabs.Tab>
            <Tabs.Tab value="initialize" leftSection={<IconPlayerPlay size={16} />} disabled={busy}>
              2 · Initialize cluster
            </Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="install" pt="md">
            <form
              onSubmit={(event) => {
                event.preventDefault()
                event.stopPropagation()
                void installForm.handleSubmit()
              }}
            >
              <Stack gap="sm">
                <installForm.Field name="hostAddress" validators={required('Host')}>
                  {(field) => (
                    <TextInput
                      label="Host address"
                      placeholder="10.0.0.12:5432"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      onBlur={field.handleBlur}
                      error={field.state.meta.errors.join(', ') || undefined}
                      disabled={install.running}
                    />
                  )}
                </installForm.Field>

                <installForm.Field name="version">
                  {(field) => (
                    <Select
                      label="PostgreSQL version"
                      data={versionData}
                      value={field.state.value}
                      onChange={(value) => field.handleChange(value ?? '')}
                      disabled={install.running}
                    />
                  )}
                </installForm.Field>

                <installForm.Field name="targetDirectory" validators={required('Target directory')}>
                  {(field) => (
                    <TextInput
                      label="Install directory"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      onBlur={field.handleBlur}
                      error={field.state.meta.errors.join(', ') || undefined}
                      disabled={install.running}
                    />
                  )}
                </installForm.Field>

                <installForm.Field name="agentToken">
                  {(field) => (
                    <PasswordInput
                      label="Agent token"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      disabled={install.running}
                    />
                  )}
                </installForm.Field>

                <StreamProgressPanel
                  events={install.events}
                  percentage={install.percentage}
                  stage={install.stage}
                  running={install.running}
                  finished={install.finished}
                  error={install.error}
                />

                <Group justify="flex-end" gap="sm">
                  {install.running ? (
                    <Button variant="default" color="red" onClick={install.cancel}>
                      Cancel run
                    </Button>
                  ) : (
                    <Button variant="default" onClick={handleClose}>
                      Close
                    </Button>
                  )}
                  {install.finished && !install.error ? (
                    <Button onClick={() => setTab('initialize')}>Continue to initialize</Button>
                  ) : (
                    <installForm.Subscribe selector={(state) => state.canSubmit}>
                      {(canSubmit) => (
                        <Button type="submit" disabled={!canSubmit} loading={install.running}>
                          Install
                        </Button>
                      )}
                    </installForm.Subscribe>
                  )}
                </Group>
              </Stack>
            </form>
          </Tabs.Panel>

          <Tabs.Panel value="initialize" pt="md">
            <form
              onSubmit={(event) => {
                event.preventDefault()
                event.stopPropagation()
                void initForm.handleSubmit()
              }}
            >
              <Stack gap="sm">
                <initForm.Field name="hostAddress" validators={required('Host')}>
                  {(field) => (
                    <TextInput
                      label="Host address"
                      placeholder="10.0.0.12:5432"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      onBlur={field.handleBlur}
                      error={field.state.meta.errors.join(', ') || undefined}
                      disabled={initialize.running}
                    />
                  )}
                </initForm.Field>

                <initForm.Field name="version">
                  {(field) => (
                    <Select
                      label="PostgreSQL version"
                      data={versionData}
                      value={field.state.value}
                      onChange={(value) => field.handleChange(value ?? '')}
                      disabled={initialize.running}
                    />
                  )}
                </initForm.Field>

                <initForm.Field name="dataDirectory" validators={required('Data directory')}>
                  {(field) => (
                    <TextInput
                      label="Data directory"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      onBlur={field.handleBlur}
                      error={field.state.meta.errors.join(', ') || undefined}
                      disabled={initialize.running}
                    />
                  )}
                </initForm.Field>

                <initForm.Field name="agentToken">
                  {(field) => (
                    <PasswordInput
                      label="Agent token"
                      value={field.state.value}
                      onChange={(event) => field.handleChange(event.currentTarget.value)}
                      disabled={initialize.running}
                    />
                  )}
                </initForm.Field>

                <StreamProgressPanel
                  events={initialize.events}
                  percentage={initialize.percentage}
                  stage={initialize.stage}
                  running={initialize.running}
                  finished={initialize.finished}
                  error={initialize.error}
                />

                <Group justify="flex-end" gap="sm">
                  {initialize.running ? (
                    <Button variant="default" color="red" onClick={initialize.cancel}>
                      Cancel run
                    </Button>
                  ) : (
                    <Button variant="default" onClick={handleClose}>
                      Close
                    </Button>
                  )}
                  <initForm.Subscribe selector={(state) => state.canSubmit}>
                    {(canSubmit) => (
                      <Button type="submit" disabled={!canSubmit} loading={initialize.running}>
                        Initialize
                      </Button>
                    )}
                  </initForm.Subscribe>
                </Group>
              </Stack>
            </form>
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Modal>
  )
}
