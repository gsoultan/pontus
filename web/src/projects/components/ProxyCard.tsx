import { Card, Group, Text, Button, ActionIcon, Stack, Code, Badge, ThemeIcon, rem, Box } from '@mantine/core';
import { IconServer, IconEdit, IconTrash, IconDatabase, IconChevronRight } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';

interface ProxyCardProps {
  projectId: string;
  proxy: any;
  isAdmin: boolean;
  onSelect: (projectId: string, proxyId: string) => void;
  onRemove: (projectId: string, proxyId: string, name: string) => void;
}

export function ProxyCard({ projectId, proxy, isAdmin, onSelect, onRemove }: ProxyCardProps) {
  return (
    <Card style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <Group justify="space-between" mb="md">
        <Group gap="sm">
          <ThemeIcon color="pontusBlue" variant="light" size="lg">
            <IconServer size={20} stroke={2} />
          </ThemeIcon>
          <Box>
            <Text fw={700} size="md">{proxy.name}</Text>
            <Badge size="xs" variant="light" color="gray" radius="sm">{proxy.balancer}</Badge>
          </Box>
        </Group>
        {isAdmin && (
          <Group gap={4}>
            <ActionIcon 
              variant="subtle" 
              size="sm"
              component={Link}
              to="/projects/$projectId/proxies/$proxyId/edit"
              params={{ projectId, proxyId: proxy.id } as any}
            >
              <IconEdit size={16} stroke={2} />
            </ActionIcon>
            <ActionIcon 
              color="red" 
              variant="subtle" 
              size="sm"
              onClick={() => onRemove(projectId, proxy.id, proxy.name)}
            >
              <IconTrash size={16} stroke={2} />
            </ActionIcon>
          </Group>
        )}
      </Group>
      
      <Stack gap="md" style={{ flex: 1 }}>
        <Box>
          <Text size="xs" c="dimmed" fw={600} mb={4}>Listen Address</Text>
          <Code block fw={600} style={{ fontSize: rem(13) }}>{proxy.address}</Code>
        </Box>

        <Box>
          <Group justify="space-between" mb="xs">
            <Text size="xs" c="dimmed" fw={600}>Backends ({proxy.backends?.length || 0})</Text>
          </Group>
          <Stack gap={6}>
            {proxy.backends?.slice(0, 3).map((backend: any) => (
              <Group key={backend.address} justify="space-between" wrap="nowrap" py={4} px={8} style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 'var(--mantine-radius-md)' }}>
                <Group gap="xs" wrap="nowrap">
                  <ThemeIcon size={16} color={backend.role === 'primary' ? 'orange' : 'indigo'} variant="light" radius="xl">
                    <IconDatabase size={10} stroke={2} />
                  </ThemeIcon>
                  <Text size="xs" fw={500} truncate>{backend.address}</Text>
                </Group>
                <Badge size="xs" variant="light" color={backend.role === 'primary' ? 'orange' : 'indigo'}>
                  {backend.role}
                </Badge>
              </Group>
            ))}
            {proxy.backends?.length > 3 && (
              <Text size="xs" c="dimmed" ta="center">+{proxy.backends.length - 3} more</Text>
            )}
            {(!proxy.backends || proxy.backends.length === 0) && (
              <Text size="xs" c="dimmed" fs="italic">No backends configured</Text>
            )}
          </Stack>
        </Box>
      </Stack>

      <Button 
        variant="light" 
        fullWidth 
        mt="lg"
        rightSection={<IconChevronRight size={16} />}
        onClick={() => onSelect(projectId, proxy.id)}
      >
        Open Dashboard
      </Button>
    </Card>
  );
}
