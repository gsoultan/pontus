import { Select, Group, Loader, ActionIcon, Tooltip, rem } from '@mantine/core';
import { IconFolder, IconPlus } from '@tabler/icons-react';
import { useProjects } from './useProjects';
import { useProjectStore } from '../store/useProjectStore';
import { useEffect } from 'react';
import { Link } from '@tanstack/react-router';

export function ProjectSelector() {
  const { data, isLoading } = useProjects();
  const { selectedProjectId, setSelectedProjectId } = useProjectStore();

  useEffect(() => {
    if (!selectedProjectId && data?.projects && data.projects.length > 0) {
      setSelectedProjectId(data.projects[0].id);
    }
  }, [data, selectedProjectId, setSelectedProjectId]);

  if (isLoading) return <Loader size="xs" />;

  const projectOptions = (data?.projects || []).map(p => ({
    value: p.id,
    label: p.name
  }));

  return (
    <Group gap="xs">
      <Select
        placeholder="Select Project"
        data={projectOptions}
        value={selectedProjectId}
        onChange={setSelectedProjectId}
        leftSection={<IconFolder size={18} stroke={1.5} />}
        size="sm"
        radius="md"
        comboboxProps={{ transitionProps: { transition: 'pop-top-left', duration: 200 }, shadow: 'md' }}
        styles={{
          input: {
            fontWeight: 600,
            width: rem(180),
            '@media (max-width: 48em)': {
              width: rem(130),
            },
            backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-8))',
          }
        }}
      />
      
      <Tooltip label="Manage Projects" withArrow position="bottom">
        <ActionIcon 
          variant="default" 
          component={Link} 
          to="/projects"
          size="lg"
        >
          <IconPlus size={20} stroke={1.5} />
        </ActionIcon>
      </Tooltip>
    </Group>
  );
}
