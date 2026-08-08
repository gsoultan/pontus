import { DEFAULT_BALANCER } from '../common/balancers'
import { createLazyFileRoute, useNavigate } from '@tanstack/react-router';
import { 
  Container, 
  Title, 
  Button, 
  Group, 
  Text, 
  Stack, 
  Badge,
  Stepper,
  Divider,
  ThemeIcon,
  SimpleGrid,
  Breadcrumbs,
  Anchor,
} from '@mantine/core';
import { 
  IconSettings, 
  IconServer, 
  IconDatabase, 
  IconCheck,
  IconArrowLeft,
  IconCircleCheck,
} from '@tabler/icons-react';
import { useState } from 'react';
import { useDisclosure } from '@mantine/hooks';
import { useProjects } from '../projects/useProjects';
import { useAddProxy } from '../projects/useAddProxy';
import { BackendModal } from '../status/components/BackendModal';
import { useBackendManagement } from '../status/hooks/useBackendManagement';
import { notifications } from '@mantine/notifications';
import { useProjectStore } from '../store/useProjectStore';
import { ProxyConfigForm } from '../projects/components/ProxyConfigForm';
import { BackendList } from '../projects/components/BackendList';

export const Route = createLazyFileRoute('/projects/$projectId/proxies/new')({
  component: NewProxyPage,
});

function NewProxyPage() {
  const { projectId } = Route.useParams();
  const navigate = useNavigate();
  const { data: projectsData } = useProjects();
  const project = projectsData?.projects?.find(p => p.id === projectId);
  
  const { mutateAsync: addProxy } = useAddProxy();
  const { addBackend } = useBackendManagement();
  const { setSelectedProjectId, setSelectedProxyId } = useProjectStore();

  const [activeStep, setActiveStep] = useState(0);
  const [proxyForm, setProxyForm] = useState({
    name: '',
    address: '',
    balancer: DEFAULT_BALANCER
  });
  
  const [backends, setBackends] = useState<any[]>([]);
  const [isSaving, setIsSaving] = useState(false);
  const [opened, { open, close }] = useDisclosure(false);
  const [editingBackend, setEditingBackend] = useState<any | null>(null);

  const handleOpenAdd = () => {
    setEditingBackend(null);
    open();
  };

  const handleOpenEdit = (backend: any) => {
    setEditingBackend(backend);
    open();
  };

  const handleBackendSubmit = (values: any) => {
    if (editingBackend) {
      setBackends(backends.map(b => b.address === values.address ? values : b));
    } else {
      if (backends.some(b => b.address === values.address)) {
        notifications.show({
          title: 'Error',
          message: 'Backend with this address already added',
          color: 'red'
        });
        return;
      }
      setBackends([...backends, values]);
    }
    close();
  };

  const handleRemoveBackendLocal = (address: string) => {
    setBackends(backends.filter(b => b.address !== address));
  };

  const handleFinalSave = async () => {
    setIsSaving(true);
    try {
      const resp = await addProxy({
        projectId,
        ...proxyForm
      } as any);
      
      const proxyId = resp.proxy?.id;
      if (proxyId) {
        setSelectedProjectId(projectId);
        setSelectedProxyId(proxyId);
        
        // Add backends sequentially
        for (const backend of backends) {
          await addBackend({ config: backend as any, projectId, proxyId });
        }
      }
      
      notifications.show({
        title: 'Success',
        message: 'Proxy and backends created successfully',
        color: 'green'
      });
      
      setActiveStep(3); // Move to completed
    } catch (error: any) {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to create proxy',
        color: 'red'
      });
    } finally {
      setIsSaving(false);
    }
  };

  const nextStep = () => setActiveStep((current) => current + 1);
  const prevStep = () => setActiveStep((current) => current - 1);

  if (!project && projectsData) {
    return (
      <Container size="md" py="xl">
        <Text>Project not found</Text>
        <Button onClick={() => navigate({ to: '/projects' })}>Back to Projects</Button>
      </Container>
    );
  }

  return (
    <Container size="md" py="md">
      <Breadcrumbs mb="xl">
        <Anchor onClick={() => navigate({ to: '/projects' })}>Projects</Anchor>
        <Text>{project?.name || 'Loading...'}</Text>
        <Text>New Proxy</Text>
      </Breadcrumbs>

      <Title order={2} mb="xl">Add New Proxy</Title>

      <Stepper active={activeStep} onStepClick={setActiveStep} allowNextStepsSelect={false}>
        <Stepper.Step label="Configuration" description="Proxy settings" icon={<IconSettings size={18} />}>
          <Stack mt="md">
            <ProxyConfigForm values={proxyForm} onChange={setProxyForm} />
            <Group justify="flex-end" mt="xl">
              <Button variant="subtle" onClick={() => navigate({ to: '/projects' })}>Cancel</Button>
              <Button onClick={nextStep} disabled={!proxyForm.name || !proxyForm.address}>Next</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Backends" description="Database nodes" icon={<IconServer size={18} />}>
          <Stack mt="md">
             <BackendList 
               backends={backends} 
               onAdd={handleOpenAdd} 
               onEdit={handleOpenEdit} 
               onRemove={handleRemoveBackendLocal} 
             />
             <Group justify="space-between" mt="xl">
               <Button variant="subtle" onClick={prevStep} leftSection={<IconArrowLeft size={16} />}>Back</Button>
               <Button onClick={nextStep} disabled={backends.length === 0}>Next</Button>
             </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Confirmation" description="Review changes" icon={<IconCircleCheck size={18} />}>
          <Stack gap="md" mt="md">
            <Title order={4}>Summary</Title>
            <SimpleGrid cols={2}>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>PROJECT</Text>
                <Text>{project?.name}</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>PROTOCOL</Text>
                <Badge variant="light">{project?.protocol}</Badge>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>PROXY NAME</Text>
                <Text>{proxyForm.name}</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>LISTEN ADDRESS</Text>
                <Text>{proxyForm.address}</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>LOAD BALANCER</Text>
                <Badge variant="dot">{proxyForm.balancer}</Badge>
              </Stack>
            </SimpleGrid>

            <Divider />
            
            <Text fw={700} size="sm">Backends to be added:</Text>
            <Stack gap="xs">
              {backends.map(b => (
                <Group key={b.address} justify="space-between">
                  <Group gap="xs">
                    <IconDatabase size={14} color="gray" />
                    <Text size="sm">{b.address}</Text>
                  </Group>
                  <Badge size="xs" variant="outline">{b.role}</Badge>
                </Group>
              ))}
              {backends.length === 0 && <Text size="xs" c="dimmed">No backends configured</Text>}
            </Stack>

            <Group justify="space-between" mt="xl">
              <Button variant="subtle" onClick={prevStep} leftSection={<IconArrowLeft size={16} />}>Back</Button>
              <Button color="green" onClick={handleFinalSave} loading={isSaving}>Save Proxy & Backends</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Completed>
          <Stack align="center" py="xl" mt="md">
             <ThemeIcon color="green" size={60} radius={60} variant="light">
               <IconCheck size={40} />
             </ThemeIcon>
             <Text fw={700} size="lg">Proxy Created Successfully!</Text>
             <Text size="sm" c="dimmed">The new proxy and all backends have been added.</Text>
             <Button onClick={() => navigate({ to: '/projects' })} mt="md">Return to Projects</Button>
          </Stack>
        </Stepper.Completed>
      </Stepper>

      <BackendModal 
        opened={opened}
        onClose={close}
        onSubmit={handleBackendSubmit}
        initialValues={editingBackend}
        title={editingBackend ? "Edit Backend Node" : "Add Backend Node"}
        hasPrimary={backends.some(b => b.role === "primary")}
      />
    </Container>
  );
}
