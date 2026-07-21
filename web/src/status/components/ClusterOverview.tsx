import { Paper, Group, Text, Stack, rem, Badge, ThemeIcon, Box } from "@mantine/core";
import { IconCrown, IconLink } from "@tabler/icons-react";
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb";

interface ClusterOverviewProps {
  backends: BackendStatus[];
  protocol: string;
}

export function ClusterOverview({ backends, protocol }: ClusterOverviewProps) {
  const primary = backends.find(b => b.role === "primary");
  const replicas = backends.filter(b => b.role === "replica");

  return (
    <Paper p="md" radius="md" withBorder bg="var(--mantine-color-body)">
      <Group mb="md" justify="space-between">
        <Group gap="sm">
          <ThemeIcon size="lg" variant="light" color="pontusBlue">
            <IconLink size={20} stroke={2} />
          </ThemeIcon>
          <Box>
            <Text fw={700} size="md">Topology Overview</Text>
            <Text size="xs" c="dimmed" fw={500}>{protocol} distribution</Text>
          </Box>
        </Group>
        <Badge variant="light" color="pontusBlue" size="sm">{backends.length} Nodes</Badge>
      </Group>

      <Box style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: rem(24), py: rem(10) }}>
        {/* Primary Node */}
        {primary ? (
          <Paper withBorder p="md" radius="md" style={{ minWidth: 240, borderTop: `${rem(3)} solid var(--mantine-color-pontusBlue-6)` }}>
            <Stack gap="sm" align="center">
              <Badge color="pontusBlue" variant="filled" leftSection={<IconCrown size={12} />} size="sm">PRIMARY</Badge>
              <Box ta="center">
                <Text fw={700} size="sm" truncate="end">{primary.address}</Text>
                <Text size="10px" c="dimmed" fw={600} mt={2}>{primary.zone || "Global Zone"}</Text>
              </Box>
              <Badge color={primary.healthy ? "successGreen" : "red"} variant="light" size="xs">{primary.healthy ? "Healthy" : "Offline"}</Badge>
            </Stack>
          </Paper>
        ) : (
          <Paper withBorder p="md" radius="md" bg="red.0" style={{ minWidth: 240, borderStyle: 'dashed' }}>
            <Text fw={700} size="sm" c="red" ta="center">No Primary Node</Text>
          </Paper>
        )}

        {/* Replica Nodes */}
        <Group justify="center" gap="md" wrap="wrap">
          {replicas.map((r, i) => (
            <Paper key={i} withBorder p="sm" radius="md" style={{ minWidth: 180, borderTop: `${rem(2)} solid var(--mantine-color-indigo-4)` }}>
              <Stack gap="xs" align="center">
                <Badge color="indigo" variant="light" size="xs">REPLICA</Badge>
                <Box ta="center">
                  <Text fw={600} size="xs" truncate="end">{r.address}</Text>
                  <Text size="10px" fw={700} c={Number(r.replicationLagMs) > 1000 ? "orange.6" : "blue.6"}>{Number(r.replicationLagMs)}ms lag</Text>
                </Box>
                <Badge color={r.healthy ? "successGreen" : "red"} variant="light" size="xs">{r.healthy ? "Up" : "Down"}</Badge>
              </Stack>
            </Paper>
          ))}
          {replicas.length === 0 && primary && (
             <Text size="xs" c="dimmed" fw={500} fs="italic">No replicas connected</Text>
          )}
        </Group>
      </Box>
    </Paper>
  );
}
