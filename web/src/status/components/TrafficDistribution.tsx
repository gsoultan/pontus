import { Badge, Box, Card, Group, Progress, Stack, Text, Tooltip, rem } from '@mantine/core'
import { IconChartBar, IconMapPin } from '@tabler/icons-react'
import type { BackendStatus } from '../../gen/api/proto/domain/status_pb'
import { EmptyState } from '../../common/components/EmptyState'
import { balancerLabel } from '../../common/balancers'

interface TrafficDistributionProps {
  backends: BackendStatus[]
  balancerType?: string
  /** The zone this proxy runs in, so local and remote can be told apart. */
  localZone?: string
}

/** A share this far from its expected value is worth pointing at. */
const DIVERGENCE_THRESHOLD = 0.15

interface Row {
  backend: BackendStatus
  requests: number
  share: number
  expected: number
  diverges: boolean
  remote: boolean
}

/**
 * Answers the one question a balancing strategy exists to answer: is traffic
 * actually going where the strategy says it should?
 *
 * Actual share is compared against the share the configured weights imply, so
 * an operator sees divergence rather than having to compute it. A strategy that
 * silently fell back — which is what happened when the picker stored a name the
 * server did not recognise — shows up here as an even split where a weighted
 * one was expected.
 */
export function TrafficDistribution({ backends, balancerType, localZone }: TrafficDistributionProps) {
  const eligible = backends.filter((b) => !b.isDraining)
  const totalRequests = eligible.reduce((sum, b) => sum + Number(b.totalRequests), 0)
  const totalWeight = eligible.reduce((sum, b) => sum + Math.max(b.weight, 1), 0)

  const rows: Row[] = eligible.map((backend) => {
    const requests = Number(backend.totalRequests)
    const share = totalRequests > 0 ? requests / totalRequests : 0
    const expected = totalWeight > 0 ? Math.max(backend.weight, 1) / totalWeight : 0
    return {
      backend,
      requests,
      share,
      expected,
      diverges: totalRequests > 100 && Math.abs(share - expected) > DIVERGENCE_THRESHOLD,
      remote: Boolean(localZone && backend.zone && backend.zone !== localZone),
    }
  })

  rows.sort((a, b) => b.requests - a.requests)

  return (
    <Card>
      <Group justify="space-between" mb="md" wrap="wrap" gap="sm">
        <Group gap="sm">
          <IconChartBar size={20} stroke={1.5} />
          <Box>
            <Text fw={700}>Traffic Distribution</Text>
            <Text size="xs" c="dimmed">
              Requests served per backend since start
            </Text>
          </Box>
        </Group>
        <Group gap={6}>
          {balancerType && (
            <Badge variant="light" color="grape" size="sm">
              {balancerLabel(balancerType)}
            </Badge>
          )}
          {localZone && (
            <Tooltip label="Backends outside this zone are penalised by the cost function">
              <Badge variant="light" color="gray" size="sm" leftSection={<IconMapPin size={12} />}>
                {localZone}
              </Badge>
            </Tooltip>
          )}
        </Group>
      </Group>

      {totalRequests === 0 ? (
        <EmptyState
          icon={IconChartBar}
          title="No traffic yet"
          description="Distribution appears once queries start flowing through the proxy."
          compact
        />
      ) : (
        <Stack gap="md">
          {rows.map(({ backend, requests, share, expected, diverges, remote }) => (
            <Box key={backend.address}>
              <Group justify="space-between" mb={4} wrap="nowrap">
                <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                  <Text size="sm" fw={600} truncate>
                    {backend.address}
                  </Text>
                  <Badge
                    size="xs"
                    variant="light"
                    color={backend.role === 'primary' ? 'pontusBlue' : 'indigo'}
                  >
                    {backend.role}
                  </Badge>
                  {remote && (
                    <Tooltip label={`In ${backend.zone}, not ${localZone} — cost is penalised`}>
                      <Badge size="xs" variant="outline" color="orange">
                        remote
                      </Badge>
                    </Tooltip>
                  )}
                </Group>
                <Group gap={8} wrap="nowrap">
                  <Text size="sm" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
                    {(share * 100).toFixed(1)}%
                  </Text>
                  <Text size="xs" c="dimmed" style={{ fontVariantNumeric: 'tabular-nums' }}>
                    {requests.toLocaleString()}
                  </Text>
                </Group>
              </Group>

              <Progress
                value={share * 100}
                color={diverges ? 'orange' : 'pontusBlue'}
                size="sm"
                radius="xl"
              />

              <Group gap={10} mt={4} wrap="wrap">
                <Text size="10px" c="dimmed">
                  weight {backend.weight} · expected {(expected * 100).toFixed(0)}%
                </Text>
                {/* Cost inputs, named as the reason rather than left as raw stats. */}
                <Text size="10px" c="dimmed">
                  {Number(backend.latencyMs)}ms latency · {backend.activeConns.toString()} active
                </Text>
                {backend.role !== 'primary' && Number(backend.replicationLagMs) > 0 && (
                  <Text size="10px" c="dimmed">
                    {Number(backend.replicationLagMs)}ms lag
                  </Text>
                )}
                {diverges && (
                  <Text size="10px" c="orange" fw={600}>
                    {share > expected ? 'above' : 'below'} its weighted share
                  </Text>
                )}
              </Group>
            </Box>
          ))}

          <Text
            size="xs"
            c="dimmed"
            pt="xs"
            style={{
              borderTop: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))`,
            }}
          >
            Expected share comes from configured weights. Latency-aware strategies deviate from it
            on purpose — a slower backend should receive less.
          </Text>
        </Stack>
      )}
    </Card>
  )
}
