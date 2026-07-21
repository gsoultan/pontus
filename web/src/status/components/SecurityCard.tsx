import { Paper, Text, Group, ThemeIcon, Stack, Grid, rem, Badge, Box } from "@mantine/core";
import { IconShieldX } from "@tabler/icons-react";
import type { FirewallStats } from "../../gen/api/proto/domain/status_pb";

interface SecurityCardProps {
  stats?: FirewallStats;
}

export function SecurityCard({ stats }: SecurityCardProps) {
  const totalBlocked = Number(stats?.blockedByWord ?? 0) + 
                      Number(stats?.blockedByPattern ?? 0) + 
                      Number(stats?.blockedBySize ?? 0);

  return (
    <Paper 
      p="md" 
      radius="md" 
      mb="lg" 
      withBorder
      bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))"
      style={{ borderLeft: `${rem(3)} solid var(--mantine-color-red-6)` }}
    >
      <Group justify="space-between" mb="md" wrap="nowrap" align="center">
        <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
          <ThemeIcon color="red" variant="light" size="lg">
            <IconShieldX size={20} stroke={1.5} />
          </ThemeIcon>
          <Box style={{ flex: 1, minWidth: 0 }}>
            <Text fw={700} size="md">Advanced SQL Firewall</Text>
            <Text size="xs" c="dimmed" fw={500}>Protection against SQL injection and exfiltration</Text>
          </Box>
        </Group>
        <Badge 
          color={totalBlocked > 0 ? "red" : "successGreen"} 
          variant="light" 
          size="sm"
        >
          {totalBlocked > 0 ? `${totalBlocked} Blocked` : "Secure"}
        </Badge>
      </Group>

      <Grid gap="sm">
        <Grid.Col span={{ base: 12, md: 4 }}>
          <Paper withBorder p="sm" radius="md" bg="var(--mantine-color-body)">
            <Stack gap={2}>
              <Text size="xs" c="dimmed" fw={600}>Keywords</Text>
              <Text fw={700} size="lg">{stats?.blockedByWord.toString() ?? "0"}</Text>
            </Stack>
          </Paper>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Paper withBorder p="sm" radius="md" bg="var(--mantine-color-body)">
            <Stack gap={2}>
              <Text size="xs" c="dimmed" fw={600}>Patterns</Text>
              <Text fw={700} size="lg">{stats?.blockedByPattern.toString() ?? "0"}</Text>
            </Stack>
          </Paper>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Paper withBorder p="sm" radius="md" bg="var(--mantine-color-body)">
            <Stack gap={2}>
              <Text size="xs" c="dimmed" fw={600}>Exfiltration</Text>
              <Text fw={700} size="lg">{stats?.blockedBySize.toString() ?? "0"}</Text>
            </Stack>
          </Paper>
        </Grid.Col>
      </Grid>
    </Paper>
  );
}
