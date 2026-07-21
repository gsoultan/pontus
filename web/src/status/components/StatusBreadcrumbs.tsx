import { Paper, Breadcrumbs, Anchor, Group, Text } from "@mantine/core";
import { IconChevronRight, IconFolder, IconServer } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { memo } from "react";

interface StatusBreadcrumbsProps {
  projectName: string | undefined;
  proxyName: string | undefined;
}

export const StatusBreadcrumbs = memo(({ projectName, proxyName }: StatusBreadcrumbsProps) => {
  return (
    <Paper withBorder p="xs" radius="md" mb="md" bg="var(--mantine-color-body)" style={{ display: 'inline-block' }}>
      <Breadcrumbs separator={<IconChevronRight size={14} stroke={2} color="var(--mantine-color-dimmed)" />} styles={{ separator: { margin: '0 8px' } }}>
        <Anchor component={Link} to="/projects" size="xs" fw={800} c="pontusBlue.7" style={{ textDecoration: 'none' }}>
          <Group gap={6}>
            <IconFolder size={14} stroke={2} />
            {projectName || "All Projects"}
          </Group>
        </Anchor>
        {proxyName && (
          <Text size="xs" fw={800} c="dimmed">
            <Group gap={6}>
              <IconServer size={14} stroke={2} />
              {proxyName}
            </Group>
          </Text>
        )}
      </Breadcrumbs>
    </Paper>
  );
});

StatusBreadcrumbs.displayName = 'StatusBreadcrumbs';
