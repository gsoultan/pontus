import { createLazyFileRoute } from '@tanstack/react-router';
import { 
  Container, 
  Button, 
  Text, 
  Stack, 
  Accordion,
  ThemeIcon,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { 
  IconPlus, 
  IconActivity,
} from '@tabler/icons-react';
import { useProjects } from '../projects/useProjects';
import { useCreateProject } from '../projects/useCreateProject';
import { useDeleteProject } from '../projects/useDeleteProject';
import { useRemoveProxy } from '../projects/useRemoveProxy';
import { useProjectStore } from '../store/useProjectStore';
import { useAuthStore } from '../store/useAuthStore';
import { ProjectItem } from '../projects/components/ProjectItem';
import { ProjectCreateModal } from '../projects/components/ProjectCreateModal';

import { PageHeader } from '../layout/components/PageHeader';

export const Route = createLazyFileRoute('/projects/')({
  component: ProjectsPage,
});

function ProjectsPage() {
  const { data, isLoading } = useProjects();
  const { mutate: createProject, isPending: isCreating } = useCreateProject();
  const { mutate: deleteProject } = useDeleteProject();
  const { mutate: removeProxy } = useRemoveProxy();
  
  const { setSelectedProjectId, setSelectedProxyId } = useProjectStore();
  const isAdmin = useAuthStore(state => state.role === 'admin');

  const [projectModalOpened, { open: openProjectModal, close: closeProjectModal }] = useDisclosure(false);

  const handleCreateProject = (values: any) => {
    createProject(values, {
      onSuccess: () => {
        closeProjectModal();
      }
    });
  };

  const handleDeleteProject = (id: string, name: string) => {
    if (confirm(`Delete project "${name}" and all its proxies?`)) {
      deleteProject(id);
    }
  };

  const handleSelectProxy = (projectId: string, proxyId: string) => {
    setSelectedProjectId(projectId);
    setSelectedProxyId(proxyId);
  };

  const handleRemoveProxy = (projectId: string, proxyId: string, name: string) => {
    if (confirm(`Remove proxy "${name}"?`)) {
      removeProxy({ projectId, proxyId });
    }
  };

  return (
    <Container size="xl" py="md">
      <PageHeader 
        title="Infrastructure"
        description="Manage your projects and database proxies"
        actions={isAdmin && (
          <Button leftSection={<IconPlus size={16} />} onClick={openProjectModal}>
            New Project
          </Button>
        )}
      />

      {isLoading ? (
        <Stack align="center" py="xl" gap="md">
          <ThemeIcon size="xl" radius="md" variant="light" color="gray">
            <IconActivity size={24} />
          </ThemeIcon>
          <Text c="dimmed" size="sm" fw={500}>Loading infrastructure topology...</Text>
        </Stack>
      ) : (
        <Accordion variant="separated" radius="md" defaultValue={data?.projects?.[0]?.id}>
          {data?.projects?.map((project) => (
            <ProjectItem
              key={project.id}
              project={project}
              isAdmin={isAdmin}
              onDeleteProject={handleDeleteProject}
              onSelectProxy={handleSelectProxy}
              onRemoveProxy={handleRemoveProxy}
            />
          ))}
        </Accordion>
      )}

      <ProjectCreateModal
        opened={projectModalOpened}
        onClose={closeProjectModal}
        onSubmit={handleCreateProject}
        isPending={isCreating}
      />
    </Container>
  );
}
