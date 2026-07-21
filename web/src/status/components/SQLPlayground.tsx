import { useState, memo, useCallback } from 'react';
import { Card, Title, Text, Button, Textarea, Stack, Group, Paper, Code, Badge, ThemeIcon, rem } from '@mantine/core';
import { IconPlayerPlay, IconInfoCircle, IconBolt } from '@tabler/icons-react';
import { statusClient } from '../services/statusService';
import type { Recommendation } from '../../gen/api/proto/domain/query_pb';
import { useProjectStore } from '../../store/useProjectStore';

export const SQLPlayground = memo(() => {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const [query, setQuery] = useState('SELECT * FROM users WHERE active = true;');
  const [plan, setPlan] = useState<string | null>(null);
  const [recommendations, setRecommendations] = useState<Recommendation[]>([]);
  const [loading, setLoading] = useState(false);

  const handleExplain = useCallback(async () => {
    if (!selectedProjectId) return;
    setLoading(true);
    try {
      const resp = await statusClient.explainQuery({ projectId: selectedProjectId, query });
      setPlan(resp.plan);
      setRecommendations(resp.recommendations);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  }, [selectedProjectId, query]);

  return (
    <Card withBorder shadow="sm" radius="lg" p="xl">
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <ThemeIcon color="pontusBlue" variant="light" size="lg" radius="md">
              <IconBolt size={20} />
            </ThemeIcon>
            <Title order={4} fw={800}>AI SQL Playground</Title>
          </Group>
          <Button 
            leftSection={<IconPlayerPlay size={16} />} 
            onClick={handleExplain} 
            loading={loading}
            variant="filled"
            color="pontusBlue"
            radius="md"
          >
            Explain Query
          </Button>
        </Group>

        <Textarea
          label={<Text size="xs" fw={800} tt="uppercase" lts={rem(0.5)} c="dimmed" mb={4}>SQL QUERY</Text>}
          placeholder="SELECT * FROM ..."
          minRows={4}
          value={query}
          onChange={(e) => setQuery(e.currentTarget.value)}
          styles={{ 
            input: { 
              fontFamily: 'monospace',
              fontSize: rem(13),
              backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6))',
            } 
          }}
        />

        {plan && (
          <Stack gap="xs">
            <Text fw={800} size="xs" tt="uppercase" lts={rem(0.5)} c="dimmed">Execution Plan</Text>
            <Paper 
              withBorder 
              p="md" 
              radius="md" 
              bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))"
            >
              <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: rem(12), backgroundColor: 'transparent' }}>{plan}</Code>
            </Paper>
          </Stack>
        )}

        {recommendations.length > 0 && (
          <Stack gap="xs">
            <Text fw={800} size="xs" tt="uppercase" lts={rem(0.5)} c="dimmed">AI Recommendations</Text>
            {recommendations.map((rec, i) => (
              <Paper 
                key={i} 
                withBorder 
                p="md" 
                bg="light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-dark-8))" 
                radius="md" 
                style={{ borderColor: 'light-dark(var(--mantine-color-pontusBlue-2), var(--mantine-color-dark-6))' }}
              >
                <Group gap="xs" mb={8}>
                  <Badge color="pontusBlue" variant="filled" size="sm" radius="sm">{rec.type}</Badge>
                  <Text fw={800} size="xs" c="pontusBlue.9" tt="uppercase" lts={rem(0.5)}>{rec.impact} Impact</Text>
                </Group>
                <Group gap="xs" align="flex-start" wrap="nowrap">
                  <IconInfoCircle size={16} color="var(--mantine-color-pontusBlue-6)" style={{ marginTop: 2, flexShrink: 0 }} />
                  <Text size="sm" fw={500}>{rec.message}</Text>
                </Group>
              </Paper>
            ))}
          </Stack>
        )}
      </Stack>
    </Card>
  );
});

SQLPlayground.displayName = 'SQLPlayground';
