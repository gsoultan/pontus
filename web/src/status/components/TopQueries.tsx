import { Table, Paper, Text, Title, Group, rem, Badge, Code, Stack, Tooltip, ActionIcon, Popover, ThemeIcon, Divider, Box } from "@mantine/core";
import { IconHistory, IconAlertTriangle, IconBrain } from "@tabler/icons-react";
import type { TopQuery } from "../../gen/api/proto/domain/query_pb";

interface TopQueriesProps {
  queries: TopQuery[];
}

export function TopQueries({ queries }: TopQueriesProps) {
  if (!queries || queries.length === 0) {
    return (
      <Paper withBorder p="xl" radius="lg" mt="xl">
        <Stack align="center" gap="xs">
          <ThemeIcon variant="light" size="xl" radius="xl" color="gray">
            <IconHistory size={24} />
          </ThemeIcon>
          <Text fw={700} c="dimmed">No query data available yet</Text>
          <Text size="sm" c="dimmed">Top queries will appear here once the proxy starts processing traffic</Text>
        </Stack>
      </Paper>
    );
  }

  return (
    <Paper withBorder p={0} radius="lg" mt="xl" style={{ overflow: 'hidden' }}>
      <Box p="lg" style={{ borderBottom: `${rem(1)} solid var(--mantine-color-gray-2)` }}>
        <Group justify="space-between">
          <Group gap="md">
            <ThemeIcon variant="light" size="lg" radius="md" color="pontusBlue">
               <IconHistory size={20} stroke={1.5} />
            </ThemeIcon>
            <Title order={3} fw={800} style={{ letterSpacing: rem(-0.5) }}>Performance Insights: Top Queries</Title>
          </Group>
          <Badge variant="light" color="pontusBlue" size="lg" radius="sm">
            Last 24 Hours
          </Badge>
        </Group>
      </Box>

      <Table.ScrollContainer minWidth={800}>
        <Table verticalSpacing="md" horizontalSpacing="lg">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>SQL Query</Table.Th>
              <Table.Th w={100} ta="right">Calls</Table.Th>
              <Table.Th w={100} ta="right">Avg Latency</Table.Th>
              <Table.Th w={100} ta="right">Peak</Table.Th>
              <Table.Th w={100}>Health</Table.Th>
              <Table.Th w={140}>Last Execution</Table.Th>
              <Table.Th w={80} ta="center">Action</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {queries.map((q, i) => (
              <Table.Tr key={i}>
                <Table.Td style={{ maxWidth: rem(500) }}>
                  <Group gap="xs" wrap="wrap">
                    <Tooltip 
                      label={q.query} 
                      multiline 
                      w={600} 
                      withArrow 
                      position="bottom-start"
                      radius="md"
                      p="md"
                    >
                      <Code 
                        block 
                        style={{ 
                          whiteSpace: 'pre-wrap', 
                          wordBreak: 'break-word',
                          maxWidth: '100%',
                          backgroundColor: 'transparent',
                          padding: 0,
                          fontSize: rem(13),
                          color: 'var(--mantine-color-text)',
                          fontWeight: 500
                        }}
                      >
                        {q.query}
                      </Code>
                    </Tooltip>
                    {q.isThrottled && (
                      <Badge color="orange" variant="light" size="xs" radius="xl">Throttled</Badge>
                    )}
                  </Group>
                </Table.Td>
                <Table.Td ta="right">
                  <Text size="sm" fw={800} c="pontusBlue.7">{q.count.toString()}</Text>
                </Table.Td>
                <Table.Td ta="right">
                  <Text size="sm" fw={700} c={Number(q.avgTimeMs) > 100 ? "orange.7" : "green.7"}>
                    {q.avgTimeMs.toString()}ms
                  </Text>
                </Table.Td>
                <Table.Td ta="right">
                  <Text size="xs" fw={500} c="dimmed">{q.maxTimeMs.toString()}ms</Text>
                </Table.Td>
                <Table.Td>
                  {Number(q.errorCount) > 0 ? (
                    <Badge color="red" variant="light" leftSection={<IconAlertTriangle size={10} />} radius="xl">
                      {q.errorCount.toString()} errors
                    </Badge>
                  ) : (
                    <Badge color="green" variant="light" radius="xl">Healthy</Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  <Stack gap={0}>
                    <Text size="xs" fw={700}>
                      {q.lastSeen ? new Date(Number(q.lastSeen.seconds) * 1000).toLocaleTimeString() : "-"}
                    </Text>
                    <Text size="10px" c="dimmed" fw={500}>
                      {q.lastSeen ? new Date(Number(q.lastSeen.seconds) * 1000).toLocaleDateString() : ""}
                    </Text>
                  </Stack>
                </Table.Td>
                <Table.Td>
                  <Group justify="center">
                    {q.insight ? (
                      <Popover width={450} position="bottom-end" withArrow shadow="xl" radius="lg">
                        <Popover.Target>
                          <ActionIcon variant="light" color="blue" radius="md" size="lg">
                            <IconBrain size={20} stroke={1.5} />
                          </ActionIcon>
                        </Popover.Target>
                        <Popover.Dropdown p="xl">
                          <Stack gap="md">
                            <Group gap="sm">
                              <ThemeIcon variant="filled" color="blue" size="lg" radius="md">
                                <IconBrain size={20} />
                              </ThemeIcon>
                              <Box>
                                <Text fw={800} size="md">AI Performance Insights</Text>
                                <Text size="xs" c="dimmed">Machine-learned query optimization suggestions</Text>
                              </Box>
                            </Group>
                            
                            <Divider />
                            
                            <Text size="xs" fw={800} c="dimmed" tt="uppercase" lts={rem(1)}>Key Recommendations</Text>
                            
                            <Stack gap="xs">
                              {q.insight.recommendations.map((r, ri) => (
                                <Paper key={ri} withBorder p="sm" radius="md">
                                  <Stack gap={4}>
                                    <Group justify="space-between">
                                      <Text fw={800} size="sm" c="blue.8">{r.type}</Text>
                                      <Badge size="xs" variant="filled" color={r.impact === 'High' ? 'red' : 'orange'}>{r.impact} Impact</Badge>
                                    </Group>
                                    <Text size="xs" fw={500}>{r.message}</Text>
                                  </Stack>
                                </Paper>
                              ))}
                            </Stack>
                            
                            {q.insight.plan && (
                              <>
                                <Divider />
                                <Text size="xs" fw={800} c="dimmed" tt="uppercase" lts={rem(1)}>Execution Strategy</Text>
                                <Code block style={{ fontSize: '11px', maxHeight: '180px', overflow: 'auto', borderRadius: 'var(--mantine-radius-md)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                                  {q.insight.plan}
                                </Code>
                              </>
                            )}
                          </Stack>
                        </Popover.Dropdown>
                      </Popover>
                    ) : (
                      <ActionIcon variant="transparent" disabled color="gray">
                        <IconBrain size={20} stroke={1.5} />
                      </ActionIcon>
                    )}
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Paper>
  );
}
