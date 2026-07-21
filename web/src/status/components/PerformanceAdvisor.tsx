import { Paper, Text, Group, ThemeIcon, Stack, Alert } from "@mantine/core";
import { IconBulb, IconAlertCircle, IconInfoCircle, IconArrowRight } from "@tabler/icons-react";
import type { PerformanceSuggestion } from "../../gen/api/proto/domain/status_pb";

interface PerformanceAdvisorProps {
  suggestions: PerformanceSuggestion[];
}

export function PerformanceAdvisor({ suggestions }: PerformanceAdvisorProps) {
  if (suggestions.length === 0) return null;

  return (
    <Paper withBorder p="md" radius="md">
      <Group justify="space-between" mb="md">
        <Group>
          <ThemeIcon color="yellow" variant="light">
            <IconBulb size={20} />
          </ThemeIcon>
          <Text fw={700}>Performance Advisor</Text>
        </Group>
      </Group>

      <Stack gap="md">
        {suggestions.map((sug, index) => (
          <Alert 
            key={index} 
            variant="light" 
            color={sug.level === "critical" ? "red" : sug.level === "warning" ? "yellow" : "blue"}
            title={sug.title}
            icon={sug.level === "critical" ? <IconAlertCircle size={16} /> : <IconInfoCircle size={16} />}
          >
            <Stack gap="xs">
              <Text size="sm">{sug.description}</Text>
              <Group gap={4}>
                <IconArrowRight size={14} />
                <Text size="xs" fw={700}>Action: {sug.action}</Text>
              </Group>
            </Stack>
          </Alert>
        ))}
      </Stack>
    </Paper>
  );
}
