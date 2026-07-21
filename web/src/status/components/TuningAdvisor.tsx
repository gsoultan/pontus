import { Paper, Title, Text, Stack, Group, ThemeIcon, Badge, Alert, Loader, Center, SimpleGrid, Card, Box, Code, rem, Button } from "@mantine/core";
import { IconAdjustmentsHorizontal, IconInfoCircle, IconAlertTriangle, IconPlayerPlay } from "@tabler/icons-react";
import { useTuneDatabase } from "../hooks/useTuneDatabase";
import { useApplyTuning } from "../hooks/useApplyTuning";

export function TuningAdvisor() {
  const { data, isLoading, error } = useTuneDatabase();
  const applyTuning = useApplyTuning();

  if (isLoading) {
    return (
      <Paper withBorder p="xl" radius="lg">
        <Center h={100}>
          <Stack align="center" gap="md">
            <Loader size="md" variant="bars" />
            <Text size="sm" c="dimmed" fw={600}>Analyzing hardware resources for optimal tuning...</Text>
          </Stack>
        </Center>
      </Paper>
    );
  }

  if (error) {
    return (
      <Alert icon={<IconAlertTriangle size={16} />} title="Analysis Failed" color="red" radius="md">
        Failed to fetch tuning recommendations: {(error as Error).message}
      </Alert>
    );
  }

  return (
    <Stack gap="xl">
      {data?.nodes.map((node, nodeIdx) => (
        <Paper key={nodeIdx} withBorder p="xl" radius="lg" shadow="xs" bg="var(--mantine-color-body)">
          <Group justify="space-between" mb="xl">
            <Group gap="md">
              <ThemeIcon color="violet" variant="light" size={42} radius="md">
                <IconAdjustmentsHorizontal size={24} stroke={1.5} />
              </ThemeIcon>
              <Stack gap={0}>
                <Title order={4} fw={800} style={{ letterSpacing: rem(-0.5) }}>
                  Node: {node.address}
                </Title>
                <Text size="sm" c="dimmed" fw={500}>Hardware-aware configuration optimization</Text>
              </Stack>
            </Group>
            <Badge color="violet" variant="filled" size="lg" radius="sm">v15/v16 Engine</Badge>
          </Group>

          <SimpleGrid cols={{ base: 1, md: 2, lg: 3 }} spacing="lg">
            {node.suggestions.map((s, i) => (
              <Card key={i} withBorder padding="md" radius="md" shadow="xs" style={{ transition: 'all 200ms ease' }}>
                <Stack gap="sm">
                  <Group justify="space-between" wrap="nowrap" align="center">
                    <Code fw={800} color="violet.0" c="violet.9" style={{ fontSize: rem(12), flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.parameter}</Code>
                    <Badge color="successGreen" variant="light" size="sm" radius="sm" fw={800} style={{ flexShrink: 0 }}>{s.suggestedValue}</Badge>
                  </Group>
                  <Text size="xs" fw={500} c="dimmed" style={{ lineHeight: 1.5 }}>{s.reason}</Text>
                  <Button
                    variant="light"
                    color="violet"
                    size="compact-xs"
                    radius="md"
                    leftSection={<IconPlayerPlay size={12} />}
                    loading={applyTuning.isPending}
                    onClick={() => applyTuning.mutate({ suggestion: s, address: node.address })}
                    style={{ alignSelf: 'flex-end' }}
                  >
                    Execute
                  </Button>
                </Stack>
              </Card>
            ))}
          </SimpleGrid>

          {node.systemChecks && node.systemChecks.length > 0 && (
            <Box mt="xl" pt="xl" style={{ borderTop: `${rem(1)} solid var(--mantine-color-gray-1)` }}>
              <Text size="xs" fw={800} c="dimmed" tt="uppercase" lts={rem(1)} mb="md">Verified System Requirements</Text>
              <SimpleGrid cols={{ base: 1, md: 2 }} spacing="xs">
                {node.systemChecks.map((check, i) => (
                  <Group key={i} gap="xs">
                    <ThemeIcon color="successGreen" size={20} radius="xl" variant="light">
                      <IconInfoCircle size={14} />
                    </ThemeIcon>
                    <Text size="sm" fw={500}>{check}</Text>
                  </Group>
                ))}
              </SimpleGrid>
            </Box>
          )}
        </Paper>
      ))}
    </Stack>
  );
}
