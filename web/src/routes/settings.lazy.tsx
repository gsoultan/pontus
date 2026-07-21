import { createLazyFileRoute } from '@tanstack/react-router'
import { Container, Paper, Text, Stack, ThemeIcon, Box, Group, Button, TextInput, NumberInput, Select, LoadingOverlay, Divider, Switch, rem, useMantineColorScheme, SimpleGrid } from "@mantine/core"
import { IconSettings, IconDeviceFloppy, IconRefresh, IconShieldLock } from "@tabler/icons-react"
import { PageHeader } from "../layout/components/PageHeader"
import { useClusterConfig } from "../status/hooks/useClusterConfig"
import { useForm } from "@mantine/form"
import { useEffect } from "react"

export const Route = createLazyFileRoute('/settings')({
  component: SettingsPage,
})

function SettingsPage() {
  const { colorScheme } = useMantineColorScheme();
  const { config, isLoading, isUpdating, updateConfig, refetch } = useClusterConfig();

  const form = useForm({
    initialValues: {
      query_timeout: '30s',
      max_conns: 1000,
      balancer: 'round-robin',
      pooling_mode: 'transaction',
      firewall_enabled: true,
      firewall_max_response_size: 10,
    },
  });

  useEffect(() => {
    if (config) {
      form.setValues({
        query_timeout: config.query_timeout || '30s',
        max_conns: parseInt(config.max_conns || '1000'),
        balancer: config.balancer || 'round-robin',
        pooling_mode: config.pooling_mode || 'transaction',
        firewall_enabled: config.firewall_enabled === 'true',
        firewall_max_response_size: parseInt(config.firewall_max_response_size || '10'),
      });
    }
  }, [config]);

  const handleSubmit = async (values: typeof form.values) => {
    await updateConfig({
      query_timeout: values.query_timeout,
      max_conns: values.max_conns.toString(),
      balancer: values.balancer,
      pooling_mode: values.pooling_mode,
      firewall_enabled: values.firewall_enabled ? 'true' : 'false',
      firewall_max_response_size: values.firewall_max_response_size.toString(),
    });
  };

  return (
    <Container size="xl" py="md">
      <PageHeader 
        title="Settings" 
        description="Global system configuration and security parameters" 
      />
      
      <Stack gap="xl" pos="relative">
        <LoadingOverlay visible={isLoading} overlayProps={{ blur: 2 }} />
        
        <form onSubmit={form.onSubmit(handleSubmit)}>
          <Stack gap="lg">
            <Paper p="md" radius="md">
              <Stack gap="md">
                <Group justify="space-between">
                  <Group gap="sm">
                    <ThemeIcon size="lg" variant="light" color="pontusBlue">
                      <IconSettings size={20} />
                    </ThemeIcon>
                    <Box>
                      <Text fw={700} size="md">Runtime Parameters</Text>
                      <Text size="xs" c="dimmed">Global cluster configuration</Text>
                    </Box>
                  </Group>
                  <Group gap="xs">
                    <Button 
                      variant="subtle" 
                      color="gray" 
                      size="sm"
                      leftSection={<IconRefresh size={16} />} 
                      onClick={() => refetch()}
                      disabled={isLoading}
                    >
                      Sync
                    </Button>
                    <Button 
                      type="submit" 
                      size="sm"
                      leftSection={<IconDeviceFloppy size={16} />} 
                      loading={isUpdating}
                    >
                      Save Changes
                    </Button>
                  </Group>
                </Group>

                <Divider />

                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                  <TextInput
                    label="Query Timeout"
                    description="Maximum time for a single query"
                    placeholder="30s"
                    {...form.getInputProps('query_timeout')}
                  />
                  <NumberInput
                    label="Global Connection Limit"
                    description="Concurrent sessions per proxy"
                    placeholder="1000"
                    {...form.getInputProps('max_conns')}
                  />
                  <Select
                    label="Balancing Strategy"
                    description="Logic for backend routing"
                    data={[
                      { value: 'round-robin', label: 'Round Robin' },
                      { value: 'least-conns', label: 'Least Connections' },
                      { value: 'random', label: 'Random' },
                      { value: 'ip-hash', label: 'IP Hash' },
                    ]}
                    {...form.getInputProps('balancer')}
                  />
                  <Select
                    label="Pooling Mode"
                    description="Database link management"
                    data={[
                      { value: 'transaction', label: 'Transaction' },
                      { value: 'session', label: 'Session' },
                      { value: 'statement', label: 'Statement' },
                    ]}
                    {...form.getInputProps('pooling_mode')}
                  />
                </SimpleGrid>
              </Stack>
            </Paper>

            <Paper p="md" radius="md">
              <Stack gap="md">
                <Group gap="sm">
                  <ThemeIcon size="lg" variant="light" color="red">
                    <IconShieldLock size={20} />
                  </ThemeIcon>
                  <Box>
                    <Text fw={700} size="md">Security Settings</Text>
                    <Text size="xs" c="dimmed">Firewall and guardrail configuration</Text>
                  </Box>
                </Group>

                <Divider />

                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                  <Paper withBorder p="sm" radius="md" bg={colorScheme === 'dark' ? 'var(--mantine-color-dark-8)' : 'var(--mantine-color-gray-0)'}>
                    <Switch
                      label="SQL Firewall"
                      description="Active deep packet inspection"
                      {...form.getInputProps('firewall_enabled', { type: 'checkbox' })}
                    />
                  </Paper>
                  <NumberInput
                    label="Max Response Size (MB)"
                    description="Limit for database responses"
                    placeholder="10"
                    {...form.getInputProps('firewall_max_response_size')}
                  />
                </SimpleGrid>
              </Stack>
            </Paper>

            <Paper p="md" radius="md" style={{ borderLeft: `${rem(3)} solid var(--mantine-color-pontusBlue-6)` }}>
               <Stack gap="xs">
                  <Text fw={700} size="sm">System Information</Text>
                  <Group gap="xl">
                    <Box>
                      <Text size="10px" c="dimmed" fw={600}>Version</Text>
                      <Text size="xs" fw={600}>v1.0.0-stable</Text>
                    </Box>
                    <Box>
                      <Text size="10px" c="dimmed" fw={600}>Runtime</Text>
                      <Text size="xs" fw={600}>Go 1.26</Text>
                    </Box>
                  </Group>
               </Stack>
            </Paper>
          </Stack>
        </form>
      </Stack>
    </Container>
  )
}
