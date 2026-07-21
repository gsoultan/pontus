import { 
  Paper, 
  Table, 
  Text, 
  Group, 
  Stack, 
  ThemeIcon, 
  Badge, 
  Tabs, 
  ScrollArea, 
  Code,
  Alert,
  Loader,
  Center
} from "@mantine/core";
import { 
  IconTerminal2, 
  IconLock, 
  IconArrowsLeftRight, 
  IconAlertCircle,
  IconDatabaseSearch
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { QueryInsight, LockInsight, ReplicationInsight } from "../../gen/api/proto/domain/query_pb";

interface PostgresInsightsProps {
  projectId: string;
  proxyId: string;
  address: string;
}

export function PostgresInsights({ projectId, proxyId, address }: PostgresInsightsProps) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['postgresInsights', projectId, proxyId, address],
    queryFn: async () => {
      const resp = await statusClient.getPostgresInsights({
        projectId,
        proxyId,
        address,
      });
      return resp;
    },
    refetchInterval: 10000, // Every 10 seconds
  });

  if (isLoading) {
    return (
      <Center h={200}>
        <Stack align="center" gap="sm">
          <Loader size="md" />
          <Text size="sm" c="dimmed">Fetching database insights...</Text>
        </Stack>
      </Center>
    );
  }

  if (error) {
    return (
      <Alert icon={<IconAlertCircle size={16} />} title="Insights Unavailable" color="red">
        Failed to fetch database insights: {(error as Error).message}
      </Alert>
    );
  }

  return (
    <Paper withBorder p="md" radius="md">
      <Group mb="md" gap="sm">
        <ThemeIcon variant="light" color="pontusBlue" size="lg">
          <IconDatabaseSearch size={20} />
        </ThemeIcon>
        <Stack gap={0}>
          <Text fw={700}>Postgres Insights</Text>
          <Text size="xs" c="dimmed">Deep visibility into database internals for {address}</Text>
        </Stack>
      </Group>

      <Tabs defaultValue="queries" color="pontusBlue">
        <Tabs.List mb="md">
          <Tabs.Tab value="queries" leftSection={<IconTerminal2 size={14} />}>Slow Queries</Tabs.Tab>
          <Tabs.Tab value="locks" leftSection={<IconLock size={14} />}>Active Locks</Tabs.Tab>
          <Tabs.Tab value="replication" leftSection={<IconArrowsLeftRight size={14} />}>Replication</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="queries">
          <ScrollArea h={400}>
            <Table striped highlightOnHover withColumnBorders>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Query</Table.Th>
                  <Table.Th style={{ textAlign: 'right' }}>Calls</Table.Th>
                  <Table.Th style={{ textAlign: 'right' }}>Total Time (ms)</Table.Th>
                  <Table.Th style={{ textAlign: 'right' }}>Mean (ms)</Table.Th>
                  <Table.Th style={{ textAlign: 'right' }}>Rows</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data?.topQueries.length === 0 ? (
                  <Table.Tr>
                    <Table.Td colSpan={5}>
                      <Text ta="center" size="sm" py="xl" c="dimmed">No query data available. Ensure pg_stat_statements is enabled.</Text>
                    </Table.Td>
                  </Table.Tr>
                ) : (
                  data?.topQueries.map((q: QueryInsight, i: number) => (
                    <Table.Tr key={i}>
                      <Table.Td>
                        <Code block style={{ maxHeight: '60px', overflow: 'hidden', textOverflow: 'ellipsis' }}>{q.query}</Code>
                      </Table.Td>
                      <Table.Td style={{ textAlign: 'right' }}>{q.calls.toString()}</Table.Td>
                      <Table.Td style={{ textAlign: 'right' }}>{q.totalTime.toFixed(2)}</Table.Td>
                      <Table.Td style={{ textAlign: 'right' }}>{q.meanTime.toFixed(2)}</Table.Td>
                      <Table.Td style={{ textAlign: 'right' }}>{q.rows.toString()}</Table.Td>
                    </Table.Tr>
                  ))
                )}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Tabs.Panel>

        <Tabs.Panel value="locks">
          <Table striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>PID</Table.Th>
                <Table.Th>Type</Table.Th>
                <Table.Th>Mode</Table.Th>
                <Table.Th>Granted</Table.Th>
                <Table.Th>Blocking Query</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data?.activeLocks.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={5}>
                    <Text ta="center" size="sm" py="xl" c="dimmed">No active lock issues detected.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                data?.activeLocks.map((l: LockInsight, i: number) => (
                  <Table.Tr key={i}>
                    <Table.Td>{l.pid}</Table.Td>
                    <Table.Td>{l.locktype}</Table.Td>
                    <Table.Td>{l.mode}</Table.Td>
                    <Table.Td>
                      <Badge color={l.granted ? "green" : "red"} size="xs">
                        {l.granted ? "Yes" : "Waiting"}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Code style={{ fontSize: '11px' }}>{l.query || "N/A"}</Code>
                    </Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Tabs.Panel>

        <Tabs.Panel value="replication">
          <Table striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>PID</Table.Th>
                <Table.Th>State</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Sent Lag (Bytes)</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Write Lag (Bytes)</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Flush Lag (Bytes)</Table.Th>
                <Table.Th style={{ textAlign: 'right' }}>Replay Lag (Bytes)</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data?.replicationStatus.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={6}>
                    <Text ta="center" size="sm" py="xl" c="dimmed">This node has no active replication streaming.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                data?.replicationStatus.map((r: ReplicationInsight, i: number) => (
                  <Table.Tr key={i}>
                    <Table.Td>{r.pid}</Table.Td>
                    <Table.Td>
                      <Badge color="blue" variant="light">{r.state}</Badge>
                    </Table.Td>
                    <Table.Td style={{ textAlign: 'right' }}>{r.sentLsn}</Table.Td>
                    <Table.Td style={{ textAlign: 'right' }}>{r.writeLsn}</Table.Td>
                    <Table.Td style={{ textAlign: 'right' }}>{r.flushLsn}</Table.Td>
                    <Table.Td style={{ textAlign: 'right' }}>{r.replayLsn}</Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Tabs.Panel>
      </Tabs>
    </Paper>
  );
}
