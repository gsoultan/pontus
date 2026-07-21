import { Paper, Text, Group, ThemeIcon, Badge, Stack, Tooltip, Box, SimpleGrid } from "@mantine/core";
import { IconActivity, IconInfoCircle, IconAlertTriangle } from "@tabler/icons-react";
import type { AdaptiveStatus as AdaptiveStatusType } from "../../gen/api/proto/domain/status_pb";

interface AdaptiveStatusCardProps {
  status?: AdaptiveStatusType;
}

export function AdaptiveStatusCard({ status }: AdaptiveStatusCardProps) {
  if (!status) return null;

  return (
    <Paper p="md" radius="md" withBorder bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))">
      <Group justify="space-between" mb="md">
        <Group gap="sm">
          <ThemeIcon color={status.isThrottled ? "orange" : "pontusBlue"} variant="light" size="lg">
            <IconActivity size={20} stroke={2} />
          </ThemeIcon>
          <Text size="xs" c="dimmed" fw={600}>Admission Control</Text>
        </Group>
        <Badge 
          color={status.isThrottled ? "orange" : "successGreen"} 
          variant="light" 
          size="sm"
        >
          {status.isThrottled ? "Throttling" : "Optimal"}
        </Badge>
      </Group>

      <Stack gap="md">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <Text size="xl" fw={700}>
              {status.isThrottled ? "Limited" : "Unrestricted"}
            </Text>
            {status.isThrottled && (
              <Tooltip label={status.throttleReason} withArrow radius="md">
                <IconAlertTriangle size={18} color="var(--mantine-color-orange-6)" stroke={2} />
              </Tooltip>
            )}
          </Group>
          <Box ta="right">
             <Text size="10px" c="dimmed" fw={600}>Status</Text>
             <Text fw={700} size="sm" c={status.isThrottled ? "orange.7" : "successGreen.7"}>
               {status.isThrottled ? "Fair" : "Excellent"}
             </Text>
          </Box>
        </Group>

        <SimpleGrid cols={2} spacing="md">
          <Box>
            <Text size="10px" c="dimmed" fw={600}>Max Queue</Text>
            <Text fw={700} size="md">{status.currentMaxWaiters}</Text>
          </Box>
          <Box style={{ textAlign: 'right' }}>
            <Text size="10px" c="dimmed" fw={600}>Active Threads</Text>
            <Text fw={700} size="md">{status.activeGoroutines.toLocaleString()}</Text>
          </Box>
        </SimpleGrid>

        {status.isThrottled && (
          <Paper p="sm" radius="md" bg="light-dark(var(--mantine-color-orange-0), rgba(255, 145, 0, 0.1))">
            <Group gap="xs" align="flex-start" wrap="nowrap">
              <IconInfoCircle size={14} color="var(--mantine-color-orange-6)" style={{ marginTop: 2 }} />
              <Text size="xs" fw={500} c="light-dark(var(--mantine-color-orange-8), var(--mantine-color-orange-1))">{status.throttleReason}</Text>
            </Group>
          </Paper>
        )}
      </Stack>
    </Paper>
  );
}
