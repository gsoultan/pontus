import { AreaChart } from '@mantine/charts';
import { Paper, Title, Group, Text, Stack } from '@mantine/core';
import { useStatus } from '../hooks/useStatus';
import { memo, useMemo } from 'react';

export const PerformanceCharts = memo(() => {
  const { data } = useStatus();
  
  const chartData = useMemo(() => data?.history.map(h => ({
    time: h.timestamp ? new Date(Number(h.timestamp.seconds) * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '',
    rps: h.requestsPerSecond,
    errors: h.errorRate * 100, // Convert to percentage
    latency: h.latencyMs,
  })) || [], [data?.history]);

  return (
    <Stack gap="md">
      <Paper withBorder p="md" radius="md" shadow="sm">
        <Group justify="space-between" mb="xl">
          <Title order={4} fw={800}>Throughput & Error Trends</Title>
          <Text size="xs" c="dimmed" fw={700}>Last 60 minutes</Text>
        </Group>
        <AreaChart
          h={300}
          data={chartData}
          dataKey="time"
          series={[
            { name: 'rps', color: 'pontusBlue.6', label: 'Requests/sec' },
            { name: 'errors', color: 'red.6', label: 'Error Rate %' },
          ]}
          curveType="monotone"
          withLegend
          tickLine="xy"
          withXAxis
          withYAxis
          gridAxis="xy"
          dotProps={{ r: 2, strokeWidth: 1 }}
          activeDotProps={{ r: 4, strokeWidth: 1 }}
        />
      </Paper>

      <Paper withBorder p="md" radius="md" shadow="sm">
        <Group justify="space-between" mb="xl">
          <Title order={4} fw={800}>Average Latency (ms)</Title>
          <Text size="xs" c="dimmed" fw={700}>Last 60 minutes</Text>
        </Group>
        <AreaChart
          h={300}
          data={chartData}
          dataKey="time"
          series={[
            { name: 'latency', color: 'successGreen.6', label: 'Avg Latency (ms)' },
          ]}
          curveType="monotone"
          withLegend
          tickLine="xy"
          withXAxis
          withYAxis
          gridAxis="xy"
          dotProps={{ r: 2, strokeWidth: 1 }}
          activeDotProps={{ r: 4, strokeWidth: 1 }}
        />
      </Paper>
    </Stack>
  );
});

PerformanceCharts.displayName = 'PerformanceCharts';
