import { SimpleGrid, Group, Button, Paper, Stack, ThemeIcon, Box, Text, Divider, rem, useMantineColorScheme } from "@mantine/core";
import { IconServer, IconPlus, IconCopy } from "@tabler/icons-react";
import { BackendCard } from "./BackendCard";
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb";
import { memo } from "react";

interface BackendsSectionProps {
  backends: BackendStatus[];
  onAdd: (role: string) => void;
  onEdit: (backend: BackendStatus) => void;
  onRemove: (address: string) => void;
  onBackup: (address: string) => void;
  onRestore: (address: string) => void;
  onVacuum: (address: string) => void;
  onRestart: (address: string) => void;
  onShutdown: (address: string) => void;
  onShowInsights: (address: string) => void;
  isAdmin?: boolean;
}

export const BackendsSection = memo(({ 
  backends, 
  onAdd, 
  onEdit, 
  onRemove, 
  onBackup, 
  onRestore, 
  onVacuum, 
  onRestart,
  onShutdown,
  onShowInsights,
  isAdmin 
}: BackendsSectionProps) => {
  const { colorScheme } = useMantineColorScheme();

  return (
    <>
      <Divider 
        my="xl" 
        label={
          <Group gap="xs">
            <IconServer size={20} color="var(--mantine-color-pontusBlue-6)" />
            <Text fw={800} size="lg">Backend Nodes</Text>
          </Group>
        } 
        labelPosition="left" 
      />

      {isAdmin && (
        <Group justify="flex-end" mb="lg">
          <Button.Group>
            <Button 
              leftSection={<IconPlus size={18} />} 
              onClick={() => onAdd('primary')}
              variant="filled"
              size="md"
            >
              Add Primary
            </Button>
            <Button 
              leftSection={<IconCopy size={18} />} 
              onClick={() => onAdd('replica')}
              variant="light"
              size="md"
            >
              Add Replica
            </Button>
          </Button.Group>
        </Group>
      )}

      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lg">
        {backends.map((backend) => (
            <BackendCard 
              key={backend.address} 
              backend={backend} 
              onEdit={onEdit}
              onRemove={onRemove}
              onBackup={onBackup}
              onRestore={onRestore}
              onVacuum={onVacuum}
              onRestart={onRestart}
              onShutdown={onShutdown}
              onShowInsights={onShowInsights}
              isAdmin={isAdmin}
            />
        ))}
      </SimpleGrid>

      {backends.length === 0 && (
        <Paper withBorder p={rem(60)} radius="md" mt="xl" bg={colorScheme === 'dark' ? 'dark.8' : 'gray.0'} style={{ borderStyle: 'dashed', borderWidth: rem(2) }}>
          <Stack align="center" gap="md">
            <ThemeIcon size={60} radius="xl" variant="light" color="gray">
               <IconServer size={34} />
            </ThemeIcon>
            <Box ta="center">
              <Text fw={800} size="lg">No backends configured</Text>
              <Text c="dimmed">Get started by adding your first database backend node</Text>
            </Box>
            {isAdmin && (
              <Button variant="outline" size="md" onClick={() => onAdd('primary')} leftSection={<IconPlus size={16} />}>
                Configure first backend
              </Button>
            )}
          </Stack>
        </Paper>
      )}
    </>
  );
});

BackendsSection.displayName = 'BackendsSection';
