import { Stack, Group, Text, Button, ActionIcon, Paper, Badge, Tooltip } from '@mantine/core';
import { IconPlus, IconDatabase, IconEdit, IconTrash } from '@tabler/icons-react';

interface BackendListProps {
  backends: any[];
  onAdd: () => void;
  onEdit: (backend: any) => void;
  onRemove: (address: string) => void;
}

export function BackendList({ backends, onAdd, onEdit, onRemove }: BackendListProps) {
  return (
    <Stack>
      <Group justify="space-between">
        <Text size="sm" fw={500}>Current Backends ({backends.length})</Text>
        <Button 
          size="xs" 
          leftSection={<IconPlus size={14} />} 
          variant="light"
          onClick={onAdd}
        >
          Add Backend
        </Button>
      </Group>
      
      <Stack gap="xs">
        {backends.map(backend => (
          <Paper key={backend.address} withBorder p="xs" radius="md">
            <Group justify="space-between">
              <Group gap="xs">
                <IconDatabase size={16} color="gray" />
                <Text size="sm" fw={600}>{backend.address}</Text>
                <Badge size="xs" variant="light" color={backend.role === 'primary' ? 'orange' : 'blue'}>{backend.role}</Badge>
                <Badge size="xs" variant="outline" color="gray">W: {backend.weight || 1}</Badge>
              </Group>
              <Group gap={5}>
                <Tooltip label="Edit Backend">
                  <ActionIcon variant="subtle" size="sm" onClick={() => onEdit(backend)}>
                    <IconEdit size={14} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="Remove Backend">
                  <ActionIcon color="red" variant="subtle" size="sm" onClick={() => onRemove(backend.address)}>
                    <IconTrash size={14} />
                  </ActionIcon>
                </Tooltip>
              </Group>
            </Group>
          </Paper>
        ))}
        {backends.length === 0 && (
          <Text size="xs" c="dimmed" fs="italic">No backends configured. Click "Add Backend" to begin.</Text>
        )}
      </Stack>
    </Stack>
  );
}
