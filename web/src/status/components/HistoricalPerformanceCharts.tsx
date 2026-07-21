import { AreaChart } from '@mantine/charts';
import { Paper, Title, Group, Text, Stack, SegmentedControl, Skeleton, Box, SimpleGrid, rem } from '@mantine/core';
import { useState, memo, useMemo } from 'react';
import { useMetricsHistory } from '../hooks/useMetricsHistory';

export const HistoricalPerformanceCharts = memo(() => {
  const [range, setRange] = useState('24');
  const { data, isLoading } = useMetricsHistory({ rangeHours: parseInt(range) });
  
  const chartData = useMemo(() => data?.history.map(h => ({
    time: h.timestamp ? new Date(Number(h.timestamp.seconds) * 1000).toLocaleString([], { 
      month: 'short', 
      day: 'numeric', 
      hour: '2-digit', 
      minute: '2-digit' 
    }) : '',
    rps: h.requestsPerSecond,
    errors: h.errorRate * 100, // Convert to percentage
    latency: h.latencyMs,
  })) || [], [data?.history]);

  if (isLoading) {
    return (
      <Stack gap="xl">
        <Group justify="space-between">
          <Stack gap={4}>
            <Skeleton height={32} width={300} radius="md" />
            <Skeleton height={20} width={200} radius="md" />
          </Stack>
          <Skeleton height={42} width={240} radius="md" />
        </Group>
        <SimpleGrid cols={{ base: 1, xl: 2 }} spacing="xl">
          <Skeleton height={450} radius="lg" />
          <Skeleton height={450} radius="lg" />
        </SimpleGrid>
      </Stack>
    );
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between">
        <Stack gap={0}>
          <Title order={3} fw={900} style={{ letterSpacing: rem(-1) }}>Historical Performance Analysis</Title>
          <Text size="sm" c="dimmed" fw={500}>System metrics trend over selected time interval</Text>
        </Stack>
        <SegmentedControl
          value={range}
          onChange={setRange}
          radius="md"
          size="md"
          data={[
            { label: '1H', value: '1' },
            { label: '6H', value: '6' },
            { label: '24H', value: '24' },
            { label: '7D', value: '168' },
          ]}
          styles={{
            root: { backgroundColor: 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-6))' },
            indicator: { boxShadow: 'var(--mantine-shadow-sm)' }
          }}
        />
      </Group>

      <SimpleGrid cols={{ base: 1, xl: 2 }} spacing="xl">
        <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)">
          <Group justify="space-between" mb="xl">
            <Box>
              <Title order={4} fw={800}>Throughput & Error Trends</Title>
              <Text size="xs" c="dimmed" fw={600} tt="uppercase" lts={rem(0.5)}>Requests per second vs Error rate (%)</Text>
            </Box>
          </Group>
          <AreaChart
            h={350}
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
            strokeWidth={3}
            dotProps={{ r: 4, strokeWidth: 2, fill: '#fff' }}
            activeDotProps={{ r: 6, strokeWidth: 2 }}
          />
        </Paper>

        <Paper withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)">
          <Group justify="space-between" mb="xl">
            <Box>
              <Title order={4} fw={800}>Latency Profile (ms)</Title>
              <Text size="xs" c="dimmed" fw={600} tt="uppercase" lts={rem(0.5)}>Average query execution time across cluster</Text>
            </Box>
          </Group>
          <AreaChart
            h={350}
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
            strokeWidth={3}
            dotProps={{ r: 4, strokeWidth: 2, fill: '#fff' }}
            activeDotProps={{ r: 6, strokeWidth: 2 }}
          />
        </Paper>
      </SimpleGrid>
    </Stack>
  );
});

HistoricalPerformanceCharts.displayName = 'HistoricalPerformanceCharts';
