import { Group, Stack, Title, Text, rem, Box, Divider } from '@mantine/core';
import type { ReactNode } from 'react';

interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumbs?: ReactNode;
}

export function PageHeader({ title, description, actions, breadcrumbs }: PageHeaderProps) {
  return (
    <Box mb="xl">
      {breadcrumbs && <Box mb="sm">{breadcrumbs}</Box>}
      <Group justify="space-between" align="flex-end" wrap="wrap" mb="md" gap="md">
        <Stack gap={2} style={{ flex: 1, minWidth: rem(280) }}>
          <Title order={1} fw={700} size="h2">
            {title}
          </Title>
          {description && (
            <Text c="dimmed" size="sm" fw={500} style={{ maxWidth: rem(700) }}>
              {description}
            </Text>
          )}
        </Stack>
        {actions && <Group gap="xs" align="center">{actions}</Group>}
      </Group>
      <Divider />
    </Box>
  );
}
