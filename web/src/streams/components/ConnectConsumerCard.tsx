import { ActionIcon, Alert, Card, Code, CopyButton, Group, List, Stack, Text, ThemeIcon, Tooltip } from '@mantine/core'
import { IconAlertTriangle, IconCheck, IconCopy, IconPlugConnected } from '@tabler/icons-react'

interface ConnectConsumerCardProps {
  /** host:port the proxy listens on, e.g. "10.0.0.5:6432". */
  proxyAddress?: string
  database?: string
}

/**
 * The handoff: what an operator actually pastes into Debezium or pg_recvlogical.
 *
 * This is the whole "how do I use it" surface. A CDC consumer is not created in
 * Pontus — it connects itself — so the useful thing the dashboard can do is
 * hand over the exact DSN and state the two rules that differ from a normal
 * connection.
 */
export function ConnectConsumerCard({ proxyAddress, database = 'your_database' }: ConnectConsumerCardProps) {
  const host = proxyAddress || 'pontus-host:6432'
  const dsn = `postgres://<user>@${host}/${database}?replication=database`

  return (
    <Card>
      <Stack gap="md">
        <Group gap="sm" align="flex-start" wrap="nowrap">
          <ThemeIcon variant="light" color="grape" size="lg" radius="md">
            <IconPlugConnected size={20} stroke={1.5} />
          </ThemeIcon>
          <Stack gap={2}>
            <Text fw={700}>Connect a CDC consumer</Text>
            <Text size="sm" c="dimmed">
              Point Debezium, pg_recvlogical or any logical-replication client at the proxy. It
              attaches itself — there is nothing to create here.
            </Text>
          </Stack>
        </Group>

        <Group gap="xs" wrap="nowrap">
          <Code block style={{ flex: 1, wordBreak: 'break-all' }}>
            {dsn}
          </Code>
          <CopyButton value={dsn} timeout={1500}>
            {({ copied, copy }) => (
              <Tooltip label={copied ? 'Copied' : 'Copy connection string'}>
                <ActionIcon variant="light" size="lg" color={copied ? 'successGreen' : 'gray'} onClick={copy} aria-label="Copy connection string">
                  {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                </ActionIcon>
              </Tooltip>
            )}
          </CopyButton>
        </Group>

        <Text size="sm" fw={600}>
          Two things behave differently from an application connection
        </Text>
        <List size="sm" spacing={6}>
          <List.Item>
            <Text span fw={600}>
              It is not load balanced.
            </Text>{' '}
            A replication slot lives on one node, so the stream is pinned to that node for its
            whole life. Only the primary serves logical replication.
          </List.Item>
          <List.Item>
            <Text span fw={600}>
              It holds capacity until it disconnects.
            </Text>{' '}
            A stream keeps its slot in the pool for hours, unlike a session that is returned
            after each transaction. That is why streams have their own budget.
          </List.Item>
        </List>

        <Alert color="orange" variant="light" icon={<IconAlertTriangle size={16} />} radius="md">
          <Text size="sm">
            <Text span fw={700}>
              Failover ends a stream.
            </Text>{' '}
            Replication slots are not copied to replicas, so a promoted node has no slot for your
            consumer. Pontus terminates the stream rather than reconnecting it — reconnecting
            would resume from the wrong position and lose changes silently. Your consumer resumes
            from its own checkpoint, which is the only place the correct position is known.
          </Text>
        </Alert>
      </Stack>
    </Card>
  )
}
