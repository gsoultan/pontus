import { Paper, Text, Group, SimpleGrid, RingProgress, ThemeIcon, Stack, Progress, rem } from "@mantine/core";
import { IconCpu, IconDeviceFloppy, IconChartBar, IconUsers } from "@tabler/icons-react";
import type { SystemMetrics as SystemMetricsType } from "../../gen/api/proto/domain/metrics_pb";

interface SystemMetricsProps {
  metrics?: SystemMetricsType;
}

export function SystemMetrics({ metrics }: SystemMetricsProps) {
  if (!metrics) return null;

  const formatBytes = (bytes: bigint | number) => {
    const b = Number(bytes);
    if (b === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(b) / Math.log(k));
    return parseFloat((b / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  return (
    <SimpleGrid cols={{ base: 1, xs: 2, md: 3, lg: 4, xl: 5 }} mb="xl" spacing="lg">
      <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)" style={{ overflow: 'hidden' }}>
        <Group justify="space-between" mb="md" wrap="nowrap">
          <Text size="xs" c="dimmed" tt="uppercase" fw={800} lts={rem(1)} truncate>CPU Load</Text>
          <ThemeIcon color="cyan" variant="light" size="lg" radius="md" style={{ flexShrink: 0 }}>
            <IconCpu size={20} stroke={1.5} />
          </ThemeIcon>
        </Group>
        <Group justify="center">
          <RingProgress
            size={120}
            thickness={12}
            roundCaps
            sections={[{ value: metrics.cpuUsagePercent, color: metrics.cpuUsagePercent > 80 ? "red.6" : "cyan.6" }]}
            label={
              <Stack gap={0} align="center">
                <Text ta="center" fw={900} size="xl" style={{ lineHeight: 1 }}>
                  {metrics.cpuUsagePercent.toFixed(0)}%
                </Text>
                <Text size="10px" c="dimmed" fw={700}>CORES</Text>
              </Stack>
            }
          />
        </Group>
      </Paper>

      <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)" style={{ overflow: 'hidden' }}>
        <Group justify="space-between" mb="md" wrap="nowrap">
          <Text size="xs" c="dimmed" tt="uppercase" fw={800} lts={rem(1)} truncate>Memory (RAM)</Text>
          <ThemeIcon color="grape" variant="light" size="lg" radius="md" style={{ flexShrink: 0 }}>
            <IconChartBar size={20} stroke={1.5} />
          </ThemeIcon>
        </Group>
        <Stack gap="sm">
          <Group justify="space-between" align="flex-end">
            <Text size={rem(28)} fw={900} style={{ lineHeight: 1 }}>{metrics.memoryUsagePercent.toFixed(1)}%</Text>
            <Text size="10px" fw={700} c="grape.7">IN USE</Text>
          </Group>
          <Progress value={metrics.memoryUsagePercent} color="grape.6" size="md" radius="xl" animated={metrics.memoryUsagePercent > 90} />
          <Text size="xs" c="dimmed" fw={600} ta="right">{formatBytes(metrics.memoryUsedBytes)} / {formatBytes(metrics.memoryTotalBytes)}</Text>
        </Stack>
      </Paper>

      <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)" style={{ overflow: 'hidden' }}>
        <Group justify="space-between" mb="md" wrap="nowrap">
          <Text size="xs" c="dimmed" tt="uppercase" fw={800} lts={rem(1)} truncate>Disk Storage</Text>
          <ThemeIcon color="orange" variant="light" size="lg" radius="md" style={{ flexShrink: 0 }}>
            <IconDeviceFloppy size={20} stroke={1.5} />
          </ThemeIcon>
        </Group>
        <Stack gap="sm">
          <Group justify="space-between" align="flex-end">
            <Text size={rem(28)} fw={900} style={{ lineHeight: 1 }}>{metrics.storageUsagePercent.toFixed(1)}%</Text>
            <Text size="10px" fw={700} c="orange.7">CAPACITY</Text>
          </Group>
          <Progress value={metrics.storageUsagePercent} color="orange.6" size="md" radius="xl" />
          <Text size="xs" c="dimmed" fw={600} ta="right">{formatBytes(metrics.storageUsedBytes)} / {formatBytes(metrics.storageTotalBytes)}</Text>
        </Stack>
      </Paper>

      <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)" style={{ overflow: 'hidden' }}>
        <Group justify="space-between" mb="md" wrap="nowrap">
          <Text size="xs" c="dimmed" tt="uppercase" fw={800} lts={rem(1)} truncate>System Load</Text>
          <ThemeIcon color="teal" variant="light" size="lg" radius="md" style={{ flexShrink: 0 }}>
            <IconChartBar size={20} stroke={1.5} />
          </ThemeIcon>
        </Group>
        <Group grow mt="sm" gap="xs">
          <Stack gap={2} align="center">
            <Text size="lg" fw={900} c="teal.8">{metrics.load1.toFixed(2)}</Text>
            <Text size="10px" fw={800} c="dimmed" tt="uppercase">1m</Text>
          </Stack>
          <Stack gap={2} align="center" style={{ borderLeft: `${rem(1)} solid var(--mantine-color-gray-2)`, borderRight: `${rem(1)} solid var(--mantine-color-gray-2)` }}>
            <Text size="lg" fw={900} c="teal.8">{metrics.load5.toFixed(2)}</Text>
            <Text size="10px" fw={800} c="dimmed" tt="uppercase">5m</Text>
          </Stack>
          <Stack gap={2} align="center">
            <Text size="lg" fw={900} c="teal.8">{metrics.load15.toFixed(2)}</Text>
            <Text size="10px" fw={800} c="dimmed" tt="uppercase">15m</Text>
          </Stack>
        </Group>
      </Paper>

      <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)" style={{ overflow: 'hidden' }}>
        <Group justify="space-between" mb="md" wrap="nowrap">
          <Text size="xs" c="dimmed" tt="uppercase" fw={800} lts={rem(1)} truncate>Concurrency</Text>
          <ThemeIcon color="indigo" variant="light" size="lg" radius="md" style={{ flexShrink: 0 }}>
            <IconUsers size={20} stroke={1.5} />
          </ThemeIcon>
        </Group>
        <Stack gap={2} mt="xs">
          <Text size={rem(28)} fw={900} style={{ lineHeight: 1, letterSpacing: rem(-0.5) }} truncate>{Number(metrics.goroutines).toLocaleString()}</Text>
          <Text size="xs" c="dimmed" fw={600} truncate>Active Go routines</Text>
          <Text size="10px" c="dimmed" fw={500} mt="xs" truncate>Total lightweight threads</Text>
        </Stack>
      </Paper>
    </SimpleGrid>
  );
}
