import { Paper, Text, Group, SimpleGrid, ThemeIcon, Stack, Progress, Badge, Divider } from "@mantine/core";
import { IconArrowsTransferDown, IconDatabase, IconActivity, IconGauge, IconAlertTriangle } from "@tabler/icons-react";
import type { SystemMetrics as SystemMetricsType } from "../../gen/api/proto/domain/metrics_pb";

interface AdvancedMetricsProps {
  metrics?: SystemMetricsType;
}

export function AdvancedMetrics({ metrics }: AdvancedMetricsProps) {
  if (!metrics) return null;

  const formatBytes = (bytes: bigint | number) => {
    const b = Number(bytes);
    if (b === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(b) / Math.log(k));
    return parseFloat((b / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const formatRate = (bytes: bigint | number) => {
    return `${formatBytes(bytes)}/s`;
  };

  const db = metrics.dbMetrics;
  const disk = metrics.diskIo;
  const net = metrics.networkIo;

  return (
    <Stack gap="lg" mb="xl">
      <Divider label={<Group gap={5}><IconDatabase size={14} /><Text fw={700}>Database Engine Stats</Text></Group>} labelPosition="left" />
      
      {db ? (
        <SimpleGrid cols={{ base: 1, md: 4 }}>
          <Paper withBorder p="md" radius="md">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Active Backends</Text>
            <Group align="flex-end" gap="xs" mt={5}>
              <Text fw={800} size="h3">{db.activeBackends.toString()}</Text>
              <Text size="sm" c="dimmed" mb={4}>/ {db.maxBackends.toString()} max</Text>
            </Group>
            <Progress 
              value={(Number(db.activeBackends) / Number(db.maxBackends)) * 100} 
              color={Number(db.activeBackends) / Number(db.maxBackends) > 0.8 ? "red" : "blue"} 
              size="sm" mt="sm" radius="xl" 
            />
          </Paper>

          <Paper withBorder p="md" radius="md">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Cache Hit Ratio</Text>
            <Group align="flex-end" gap="xs" mt={5}>
              <Text fw={800} size="h3">{(db.cacheHitRatio * 100).toFixed(2)}%</Text>
            </Group>
            <Text size="xs" c={db.cacheHitRatio > 0.95 ? "green" : "orange"} mt="sm" fw={600}>
              {db.cacheHitRatio > 0.95 ? "Optimal performance" : "Possible cache pressure"}
            </Text>
          </Paper>

          <Paper withBorder p="md" radius="md">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Transactions</Text>
            <Stack gap={2} mt={5}>
              <Group justify="space-between">
                <Text size="sm">Committed</Text>
                <Text size="sm" fw={700} c="green">{db.transactionsCommitted.toString()}</Text>
              </Group>
              <Group justify="space-between">
                <Text size="sm">Rolled back</Text>
                <Text size="sm" fw={700} c="red">{db.transactionsRolledBack.toString()}</Text>
              </Group>
            </Stack>
          </Paper>

          <Paper withBorder p="md" radius="md">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Health Alerts</Text>
            <Stack gap={4} mt="xs">
              {Number(db.deadlocks) > 0 && (
                <Badge color="red" variant="light" leftSection={<IconAlertTriangle size={12}/>}>
                  {db.deadlocks.toString()} Deadlocks
                </Badge>
              )}
              {Number(db.conflicts) > 0 && (
                <Badge color="orange" variant="light" leftSection={<IconAlertTriangle size={12}/>}>
                  {db.conflicts.toString()} Conflicts
                </Badge>
              )}
              {db.isRecovery && (
                <Badge color="blue" variant="light" leftSection={<IconActivity size={12}/>}>
                  Lag: {formatBytes(db.replicationLagBytes)}
                </Badge>
              )}
              {!db.isRecovery && Number(db.deadlocks) === 0 && Number(db.conflicts) === 0 && (
                <Text size="xs" c="green">No critical issues detected</Text>
              )}
            </Stack>
          </Paper>
        </SimpleGrid>
      ) : (
        <Paper withBorder p="md" radius="md" bg="gray.0">
          <Text c="dimmed" size="sm" ta="center">Database metrics unavailable. Ensure Agent is connected.</Text>
        </Paper>
      )}

      <Divider label={<Group gap={5}><IconGauge size={14} /><Text fw={700}>I/O & Network throughput</Text></Group>} labelPosition="left" />

      <SimpleGrid cols={{ base: 1, md: 2 }}>
        <Paper withBorder p="md" radius="md">
          <Group justify="space-between" mb="xs">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Disk I/O</Text>
            <ThemeIcon color="blue" variant="light" size="sm">
              <IconGauge size={14} />
            </ThemeIcon>
          </Group>
          {disk ? (
            <SimpleGrid cols={2}>
              <Stack gap={0}>
                <Text size="xs" c="dimmed">Read Rate</Text>
                <Text fw={800} size="lg">{formatRate(disk.readBytesPerSec)}</Text>
                <Text size="xs" c="dimmed">{disk.readCountPerSec.toString()} ops/s</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed">Write Rate</Text>
                <Text fw={800} size="lg">{formatRate(disk.writeBytesPerSec)}</Text>
                <Text size="xs" c="dimmed">{disk.writeCountPerSec.toString()} ops/s</Text>
              </Stack>
            </SimpleGrid>
          ) : <Text size="xs" c="dimmed">Loading...</Text>}
        </Paper>

        <Paper withBorder p="md" radius="md">
          <Group justify="space-between" mb="xs">
            <Text size="xs" c="dimmed" tt="uppercase" fw={700}>Network I/O</Text>
            <ThemeIcon color="teal" variant="light" size="sm">
              <IconArrowsTransferDown size={14} />
            </ThemeIcon>
          </Group>
          {net ? (
            <SimpleGrid cols={2}>
              <Stack gap={0}>
                <Text size="xs" c="dimmed">Received</Text>
                <Text fw={800} size="lg">{formatRate(net.bytesRecvPerSec)}</Text>
                <Text size="xs" c="dimmed">{net.packetsRecvPerSec.toString()} pkts/s</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed">Sent</Text>
                <Text fw={800} size="lg">{formatRate(net.bytesSentPerSec)}</Text>
                <Text size="xs" c="dimmed">{net.packetsSentPerSec.toString()} pkts/s</Text>
              </Stack>
            </SimpleGrid>
          ) : <Text size="xs" c="dimmed">Loading...</Text>}
        </Paper>
      </SimpleGrid>
    </Stack>
  );
}
