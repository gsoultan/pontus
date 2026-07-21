import { Paper, Text, Group, ThemeIcon, rem, Skeleton, Badge, Box } from '@mantine/core';
import { IconTrendingUp, IconTrendingDown } from '@tabler/icons-react';
import type { ReactNode } from 'react';

interface StatCardProps {
  label: string;
  value: string | number;
  icon: ReactNode;
  color?: string;
  description?: string;
  trend?: {
    value: number;
    isUp: boolean;
  };
  loading?: boolean;
}

export function StatCard({ label, value, icon, color = 'blue', description, trend, loading }: StatCardProps) {
  if (loading) {
    return (
      <Paper p="md" radius="md" withBorder>
        <Skeleton height={12} width="40%" mb="sm" />
        <Skeleton height={32} width="60%" mb="xs" />
        <Skeleton height={14} width="80%" />
      </Paper>
    );
  }

  return (
    <Paper 
      p="md" 
      radius="md" 
      withBorder 
      style={{ 
        transition: 'all 200ms ease',
        backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
        height: '100%',
      }}
    >
      <Box>
        <Group justify="space-between" mb="sm" wrap="nowrap" align="flex-start">
          <Text size="xs" c="dimmed" fw={600} truncate="end" style={{ flex: 1 }}>
            {label}
          </Text>
          <ThemeIcon 
            color={color} 
            variant="light" 
            size="md" 
            radius="md" 
          >
            {icon}
          </ThemeIcon>
        </Group>

        <Group align="flex-end" gap="xs" wrap="nowrap">
          <Text fw={700} size="xl" style={{ lineHeight: 1, letterSpacing: rem(-0.5) }}>
            {value}
          </Text>
          {trend && (
            <Badge 
              variant="light" 
              color={trend.isUp ? 'successGreen' : 'red'} 
              size="sm" 
              radius="sm"
              mb={rem(2)}
              leftSection={trend.isUp ? <IconTrendingUp size={12} /> : <IconTrendingDown size={12} />}
            >
              {trend.value}%
            </Badge>
          )}
        </Group>
      </Box>

      {description && (
        <Text size="xs" c="dimmed" mt="md" fw={500}>
          {description}
        </Text>
      )}
    </Paper>
  );
}
