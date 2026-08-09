import { Badge, Group, Paper, Stack, Text, Tooltip } from "@mantine/core"
import { IconAlertTriangle, IconCircleCheck, IconClockPause } from "@tabler/icons-react"
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb"

type ReplicaHealthProps = {
  backend: BackendStatus
}

/**
 * The three states a replica can be in, as far as reads are concerned.
 *
 * These are separated because the dangerous one does not look dangerous. A
 * replica whose WAL receiver has gone is up, healthy and answering queries —
 * every dashboard signal stays green — and every row it returns is older than
 * the last. It even reports zero lag, because it has replayed everything it
 * received and then stopped receiving. Showing "0ms" next to a green dot would
 * be actively misleading, so streaming is surfaced on its own.
 *
 * "Streaming but not serving reads" is the third state, and it is deliberate
 * rather than broken: a replica that has just reconnected waits out the
 * reattach interval before traffic returns to it. Without saying so, an
 * operator sees a healthy replica getting no reads and goes looking for a
 * routing fault.
 */
function replicaState(backend: BackendStatus) {
  if (!backend.streaming) {
    return {
      color: "red",
      icon: <IconAlertTriangle size={12} />,
      label: "Not streaming",
      detail:
        "No WAL receiver is attached to a primary. This node is up and answering, " +
        "but its data stops advancing and reads served from it get older. " +
        "Its reported lag is not meaningful in this state.",
    } as const
  }

  if (!backend.readEligible) {
    return {
      color: "yellow",
      icon: <IconClockPause size={12} />,
      label: "Rejoining",
      detail:
        "Streaming again and waiting out the reattach interval before reads " +
        "return to it. A replica that reconnects and drops repeatedly would " +
        "otherwise keep pulling traffic onto data about to go stale.",
    } as const
  }

  return {
    color: "successGreen",
    icon: <IconCircleCheck size={12} />,
    label: "Serving reads",
    detail: "Streaming from a primary and within the configured lag threshold.",
  } as const
}

/**
 * Replication health for a single replica.
 *
 * Rendered only for replicas: a primary has no upstream to stream from, and
 * showing it an empty "not streaming" state would train operators to ignore
 * the badge that matters.
 */
export function ReplicaHealth({ backend }: ReplicaHealthProps) {
  const state = replicaState(backend)
  const lagMs = Number(backend.replicationLagMs)

  return (
    <Paper
      p="sm"
      radius="md"
      withBorder
      bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))"
    >
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap">
          <Text size="xs" fw={600} c="dimmed">
            Replication
          </Text>
          <Tooltip label={state.detail} multiline w={280} withArrow>
            <Badge variant="light" color={state.color} size="sm" leftSection={state.icon}>
              {state.label}
            </Badge>
          </Tooltip>
        </Group>

        <Group justify="space-between" align="baseline" wrap="nowrap">
          <Text size="xs" c="dimmed">
            Lag
          </Text>
          {backend.streaming ? (
            <Text fw={700} size="sm" c={lagMs > 5000 ? "orange" : undefined}>
              {formatLag(lagMs)}
            </Text>
          ) : (
            // Never show a lag figure for a node that is not streaming. It reads
            // as zero precisely because it stopped receiving WAL, which would
            // make the worst case look like the best one.
            <Text fw={600} size="sm" c="dimmed">
              unknown
            </Text>
          )}
        </Group>
      </Stack>
    </Paper>
  )
}

function formatLag(ms: number) {
  if (!Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.round(ms / 60_000)}m`
}
