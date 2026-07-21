import { Title, Group, Stack, SegmentedControl, Loader, Center } from '@mantine/core';
import { useState } from 'react';
import { useTopQueriesHistory } from '../hooks/useTopQueriesHistory';
import { TopQueries } from './TopQueries';
import { IconHistory } from '@tabler/icons-react';

export function HistoricalTopQueries() {
  const [range, setRange] = useState('24');
  const { data, isLoading } = useTopQueriesHistory({ rangeHours: parseInt(range) });
  
  if (isLoading) {
    return (
      <Center h={400}>
        <Loader size="lg" />
      </Center>
    );
  }

  return (
    <Stack gap="md">
      <Group justify="space-between" mb="md">
        <Group>
          <IconHistory size={24} color="var(--mantine-color-blue-filled)" />
          <Title order={3} fw={800}>Historical Top Queries</Title>
        </Group>
        <SegmentedControl
          value={range}
          onChange={setRange}
          data={[
            { label: '1H', value: '1' },
            { label: '6H', value: '6' },
            { label: '24H', value: '24' },
            { label: '7D', value: '168' },
          ]}
        />
      </Group>

      <TopQueries queries={data?.topQueries || []} />
    </Stack>
  );
}
