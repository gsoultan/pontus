import { Grid, Paper, Group, ThemeIcon, Stack, Text, Divider, rem, Box } from "@mantine/core";
import { IconServer, IconActivity, IconAlertTriangle, IconClock, IconBolt } from "@tabler/icons-react";
import { StatCard } from "../../layout/components/StatCard";
import type { GetStatusResponse } from "../../gen/api/proto/endpoints/management_pb";
import { memo } from "react";

interface StatusStatsProps {
  data: GetStatusResponse | undefined;
}

export const StatusStats = memo(({ data }: StatusStatsProps) => {
  const healthyCount = data?.backends.filter(b => b.healthy).length ?? 0;
  const totalCount = data?.backends.length ?? 0;

  const formatUptime = (seconds: bigint | number) => {
    const s = Number(seconds);
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (d > 0) return `${d}d ${h}h`;
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m ${s % 60}s`;
  };

  return (
    <Stack gap="md">
      <Grid gap="md">
        <Grid.Col span={{ base: 12, sm: 6, lg: 3 }}>
          <StatCard 
            label="Backend Health" 
            value={`${healthyCount}/${totalCount}`}
            icon={<IconServer size={20} stroke={2} />}
            color={healthyCount === totalCount ? "successGreen" : "orange"}
            description={healthyCount === totalCount ? "Cluster is healthy" : `${totalCount - healthyCount} degraded`}
          />
        </Grid.Col>

        <Grid.Col span={{ base: 12, sm: 6, lg: 3 }}>
          <StatCard 
            label="Throughput" 
            value={data?.requestsPerSecond.toFixed(1) ?? "0.0"}
            icon={<IconActivity size={20} stroke={2} />}
            color="blue"
            description="Requests per second"
          />
        </Grid.Col>

        <Grid.Col span={{ base: 12, sm: 6, lg: 3 }}>
          <StatCard 
            label="Error Rate" 
            value={`${data?.totalRequests && Number(data.totalRequests) > 0 
              ? ((Number(data.totalErrors) / Number(data.totalRequests)) * 100).toFixed(2) 
              : "0.00"}%`}
            icon={<IconAlertTriangle size={20} stroke={2} />}
            color={Number(data?.totalErrors) > 0 ? "red" : "gray"}
            description={`${data?.totalErrors.toString() || '0'} total errors`}
          />
        </Grid.Col>

        <Grid.Col span={{ base: 12, sm: 6, lg: 3 }}>
          <StatCard 
            label="System Uptime" 
            value={formatUptime(data?.uptimeSeconds ?? 0)}
            icon={<IconClock size={20} stroke={2} />}
            color="indigo"
            description="Stable operation time"
          />
        </Grid.Col>
      </Grid>

      {data?.cacheStats && (
        <Paper 
          p="md" 
          radius="md" 
          withBorder
          style={{ borderLeft: `${rem(3)} solid var(--mantine-color-pontusBlue-6)` }}
        >
          <Group justify="space-between" wrap="wrap" gap="md">
            <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: rem(250) }}>
              <ThemeIcon color="pontusBlue" variant="light" size="lg">
                <IconBolt size={20} stroke={2} />
              </ThemeIcon>
              <Box>
                <Text fw={700} size="md">Query Cache</Text>
                <Text size="xs" c="dimmed">Results served from memory</Text>
              </Box>
            </Group>
            <Group gap="lg" wrap="nowrap">
              <Box ta="center">
                <Text fw={700} size="lg" c="pontusBlue.6">{(data.cacheStats.hitRatio * 100).toFixed(1)}%</Text>
                <Text size="xs" c="dimmed">Hit Ratio</Text>
              </Box>
              <Divider orientation="vertical" h={30} />
              <Box ta="center">
                <Text fw={700} size="md">{data.cacheStats.hits.toString()}</Text>
                <Text size="xs" c="dimmed">Total Hits</Text>
              </Box>
              <Box ta="center">
                <Text fw={700} size="md">{data.cacheStats.itemsCount.toString()}</Text>
                <Text size="xs" c="dimmed">Cached</Text>
              </Box>
            </Group>
          </Group>
        </Paper>
      )}
    </Stack>
  );
});

StatusStats.displayName = 'StatusStats';
