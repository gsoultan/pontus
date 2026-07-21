import { Accordion, Group, ThemeIcon, Text, Stack, Badge, ActionIcon, rem, SimpleGrid, Button, Paper, useMantineColorScheme } from '@mantine/core';
import { IconFolder, IconTrash, IconPlus } from '@tabler/icons-react';
import { Link } from '@tanstack/react-router';
import { ProxyCard } from './ProxyCard';

interface ProjectItemProps {
  project: any;
  isAdmin: boolean;
  onDeleteProject: (id: string, name: string) => void;
  onSelectProxy: (projectId: string, proxyId: string) => void;
  onRemoveProxy: (projectId: string, proxyId: string, name: string) => void;
}

export function ProjectItem({ 
  project, 
  isAdmin, 
  onDeleteProject, 
  onSelectProxy, 
  onRemoveProxy 
}: ProjectItemProps) {
  const { colorScheme } = useMantineColorScheme();
  return (
    <Accordion.Item key={project.id} value={project.id} style={{ border: 'none', marginBottom: rem(16) }}>
      <Accordion.Control style={{ borderRadius: 'var(--mantine-radius-lg)', border: '1px solid var(--mantine-color-default-border)' }}>
        <Group justify="space-between" wrap="nowrap" style={{ width: '100%', paddingRight: rem(20) }}>
          <Group gap="lg">
            <ThemeIcon color="pontusBlue" variant="light" size={44} radius="md">
              <IconFolder size={24} stroke={2} />
            </ThemeIcon>
            <div>
              <Text fw={900} size="lg" style={{ letterSpacing: rem(-0.5) }}>{project.name}</Text>
              <Text size="xs" c="dimmed" fw={700} tt="uppercase" lts={rem(1)}>{project.id}</Text>
            </div>
          </Group>
          <Group gap={rem(40)} visibleFrom="sm">
            <Stack gap={0} align="center">
              <Badge variant="dot" color="pontusBlue" size="lg" radius="sm">{project.protocol}</Badge>
              <Text size="10px" c="dimmed" fw={800} tt="uppercase" lts={rem(1)} mt={4}>Protocol</Text>
            </Stack>
            <Stack gap={0} align="center">
              <Text fw={900} size="xl" style={{ lineHeight: 1 }}>{project.proxies?.length || 0}</Text>
              <Text size="10px" c="dimmed" fw={800} tt="uppercase" lts={rem(1)} mt={4}>Proxies</Text>
            </Stack>
            {isAdmin && (
              <ActionIcon 
                color="red" 
                variant="subtle" 
                size="lg"
                onClick={(e) => {
                  e.stopPropagation();
                  onDeleteProject(project.id, project.name);
                }}
              >
                <IconTrash size={20} />
              </ActionIcon>
            )}
          </Group>
        </Group>
      </Accordion.Control>
      <Accordion.Panel pt="md">
        <Stack gap="lg" py="xs">
          <Group justify="space-between">
            <Text fw={800} size="md" tt="uppercase" lts={rem(1)} c="dimmed">Configured Proxies</Text>
            {isAdmin && (
              <Button 
                variant="light" 
                size="xs" 
                leftSection={<IconPlus size={14} />}
                component={Link}
                to="/projects/$projectId/proxies/new"
                params={{ projectId: project.id } as any}
              >
                Add Proxy
              </Button>
            )}
          </Group>
          
          <SimpleGrid cols={{ base: 1, md: 2, lg: 3 }} spacing="lg">
            {project.proxies?.map((proxy: any) => (
              <ProxyCard
                key={proxy.id}
                projectId={project.id}
                proxy={proxy}
                isAdmin={isAdmin}
                onSelect={onSelectProxy}
                onRemove={onRemoveProxy}
              />
            ))}
          </SimpleGrid>
          
          {(!project.proxies || project.proxies.length === 0) && (
            <Paper withBorder p="xl" radius="lg" style={{ borderStyle: 'dashed', borderWidth: 2 }} bg={colorScheme === 'dark' ? 'var(--mantine-color-dark-8)' : 'gray.0'}>
              <Stack align="center" gap="xs">
                <Text fw={700} c="dimmed">No proxies configured for this project.</Text>
                {isAdmin && (
                  <Button 
                    variant="outline" 
                    size="xs" 
                    component={Link}
                    to="/projects/$projectId/proxies/new"
                    params={{ projectId: project.id } as any}
                  >
                    Configure first proxy
                  </Button>
                )}
              </Stack>
            </Paper>
          )}
        </Stack>
      </Accordion.Panel>
    </Accordion.Item>
  );
}
