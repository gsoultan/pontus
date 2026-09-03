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
  LoadingOverlay,
} from '@mantine/core';
import { 
  IconSettings, 
  IconServer, 
  IconDatabase, 
  IconCheck,
  IconArrowLeft,
  IconCircleCheck,
} from '@tabler/icons-react';
import { useState, useEffect } from 'react';
import { useDisclosure } from '@mantine/hooks';
import { useProjects } from '../projects/useProjects';
import { useUpdateProxy } from '../projects/useUpdateProxy';
import { BackendModal } from '../status/components/BackendModal';
import { useBackendManagement } from '../status/hooks/useBackendManagement';
import { notifications } from '@mantine/notifications';
import { useProjectStore } from '../store/useProjectStore';
import { ProxyConfigForm } from '../projects/components/ProxyConfigForm';
import { BackendList } from '../projects/components/BackendList';

export const Route = createLazyFileRoute('/projects/$projectId/proxies/$proxyId/edit')({
  component: EditProxyPage,
});

function EditProxyPage() {
  const { projectId, proxyId } = Route.useParams();
  const navigate = useNavigate();
  const { data: projectsData, isLoading: isLoadingProjects } = useProjects();
  const { mutateAsync: updateProxy } = useUpdateProxy();
  const { addBackend, removeBackend } = useBackendManagement();
  const { setSelectedProjectId, setSelectedProxyId } = useProjectStore();

  const [activeStep, setActiveStep] = useState(0);
  const [proxyForm, setProxyForm] = useState({
    name: '',
    address: '',
    balancer: 'round_robin'
  });
  
  const [initialBackends, setInitialBackends] = useState<any[]>([]);
  const [backends, setBackends] = useState<any[]>([]);
  const [isSaving, setIsSaving] = useState(false);
  // Which proxy the form currently holds, rather than a bare "have I loaded"
  // flag. A boolean stayed true when the route params changed, so navigating
  // from one proxy's edit page to another kept the first one's values.
  const [loadedProxyId, setLoadedProxyId] = useState<string | null>(null);
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

  const project = projectsData?.projects?.find(p => p.id === projectId);
  const proxy = project?.proxies?.find(p => p.id === proxyId);

  // Loading the form is an adjustment to state, not a side effect, so it
  // happens during render rather than in an effect.
  //
  // React re-renders immediately without committing the first pass, so there is
  // no flash of empty fields and no second paint — which is what "calling
  // setState synchronously within an effect can trigger cascading renders" is
  // warning about. See "You Might Not Need an Effect" in the React docs.
  if (proxy && loadedProxyId !== proxyId) {
    setLoadedProxyId(proxyId);
    setProxyForm({
      name: proxy.name,
      address: proxy.address,
      balancer: proxy.balancer
    });
    const bks = proxy.backends || [];
    setInitialBackends([...bks]);
    setBackends([...bks]);
  }

  // Telling the store which proxy is on screen *is* a side effect: it reaches
  // outside this component, so it belongs in an effect.
  useEffect(() => {
    setSelectedProjectId(projectId);
    setSelectedProxyId(proxyId);
  }, [projectId, proxyId, setSelectedProjectId, setSelectedProxyId]);

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
      const toAdd = backends.filter(b => !initialBackends.some(ib => ib.address === b.address));
      const toRemove = initialBackends.filter(ib => !backends.some(b => b.address === ib.address));
      
      for (const b of toRemove) {
        await removeBackend({ address: b.address, projectId, proxyId });
      }
      
      for (const b of toAdd) {
        await addBackend({ config: b as any, projectId, proxyId });
      }

      await updateProxy({
        projectId,
        proxy: {
          id: proxyId,
          ...proxyForm,
          backends: backends
        }
      } as any);
      
      notifications.show({
        title: 'Success',
        message: 'Proxy updated successfully',
        color: 'green'
      });
      
      setActiveStep(3);
    } catch (error: any) {
      notifications.show({
        title: 'Error',
        message: error.message || 'Failed to update proxy',
        color: 'red'
      });
    } finally {
      setIsSaving(false);
    }
  };

  const nextStep = () => setActiveStep((current) => current + 1);
  const prevStep = () => setActiveStep((current) => current - 1);

  if (isLoadingProjects) {
    return <LoadingOverlay visible />;
  }

  if (!proxy) {
    return (
      <Container size="md" py="xl">
        <Text>Proxy not found</Text>
        <Button onClick={() => navigate({ to: '/projects' })}>Back to Projects</Button>
      </Container>
    );
  }

  return (
    <Container size="md" py="md">
      <Breadcrumbs mb="xl">
        <Anchor onClick={() => navigate({ to: '/projects' })}>Projects</Anchor>
        <Text>{project?.name}</Text>
        <Text>Edit Proxy: {proxy.name}</Text>
      </Breadcrumbs>

      <Title order={2} mb="xl">Edit Proxy</Title>

      <Stepper active={activeStep} onStepClick={setActiveStep} allowNextStepsSelect={false}>
        <Stepper.Step label="Configuration" description="Update settings" icon={<IconSettings size={18} />}>
          <Stack mt="md">
            <ProxyConfigForm values={proxyForm} onChange={setProxyForm} />
            <Group justify="flex-end" mt="xl">
              <Button variant="subtle" onClick={() => navigate({ to: '/projects' })}>Cancel</Button>
              <Button onClick={nextStep} disabled={!proxyForm.name || !proxyForm.address}>Next</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Step label="Backends" description="Manage database nodes" icon={<IconServer size={18} />}>
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
            <Title order={4}>Summary of Changes</Title>
            <SimpleGrid cols={2}>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>PROXY NAME</Text>
                <Text>{proxyForm.name} {proxyForm.name !== proxy.name && <Badge size="xs" color="blue" ml="xs">Updated</Badge>}</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>LISTEN ADDRESS</Text>
                <Text>{proxyForm.address} {proxyForm.address !== proxy.address && <Badge size="xs" color="blue" ml="xs">Updated</Badge>}</Text>
              </Stack>
              <Stack gap={0}>
                <Text size="xs" c="dimmed" fw={700}>LOAD BALANCER</Text>
                <Badge variant="dot" color={proxyForm.balancer !== proxy.balancer ? "blue" : "gray"}>
                  {proxyForm.balancer}
                </Badge>
              </Stack>
            </SimpleGrid>

            <Divider />
            
            <Text fw={700} size="sm">Backends sync:</Text>
            <Stack gap="xs">
              {backends.filter(b => !initialBackends.some(ib => ib.address === b.address)).map(b => (
                 <Group key={b.address} justify="space-between">
                   <Group gap="xs">
                     <IconDatabase size={14} color="green" />
                     <Text size="sm">{b.address}</Text>
                   </Group>
                   <Badge size="xs" color="green" variant="light">Add</Badge>
                 </Group>
              ))}
              {initialBackends.filter(ib => !backends.some(b => b.address === ib.address)).map(b => (
                 <Group key={b.address} justify="space-between">
                   <Group gap="xs">
                     <IconDatabase size={14} color="red" />
                     <Text size="sm">{b.address}</Text>
                   </Group>
                   <Badge size="xs" color="red" variant="light">Remove</Badge>
                 </Group>
              ))}
              {backends.length === initialBackends.length && 
               backends.every(b => initialBackends.some(ib => ib.address === b.address)) && (
                 <Text size="xs" c="dimmed">No changes to backends</Text>
              )}
            </Stack>

            <Group justify="space-between" mt="xl">
              <Button variant="subtle" onClick={prevStep} leftSection={<IconArrowLeft size={16} />}>Back</Button>
              <Button color="green" onClick={handleFinalSave} loading={isSaving}>Save Changes</Button>
            </Group>
          </Stack>
        </Stepper.Step>

        <Stepper.Completed>
          <Stack align="center" py="xl" mt="md">
             <ThemeIcon color="green" size={60} radius={60} variant="light">
               <IconCheck size={40} />
             </ThemeIcon>
             <Text fw={700} size="lg">Proxy Updated Successfully!</Text>
             <Text size="sm" c="dimmed">All configuration changes have been applied.</Text>
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
