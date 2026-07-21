import { 
  Card, 
  Text, 
  Badge, 
  Group, 
  Stack, 
  ActionIcon, 
  Menu, 
  rem, 
  ThemeIcon, 
  Progress, 
  Box, 
  Divider, 
  Paper, 
  useMantineColorScheme,
  SimpleGrid
} from "@mantine/core";
import { 
  IconDatabase, 
  IconDotsVertical, 
  IconEdit, 
  IconTrash, 
  IconClock, 
  IconActivity,
  IconNetwork,
  IconSettingsAutomation,
  IconDownload,
  IconUpload,
  IconWand,
  IconArrowUpCircle,
  IconRefresh,
  IconPower,
  IconSearch
} from "@tabler/icons-react";
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb";

interface BackendCardProps {
  backend: BackendStatus;
  onEdit?: (backend: BackendStatus) => void;
  onRemove?: (address: string) => void;
  onBackup?: (address: string) => void;
  onRestore?: (address: string) => void;
  onVacuum?: (address: string) => void;
  onRestart?: (address: string) => void;
  onShutdown?: (address: string) => void;
  onShowInsights?: (address: string) => void;
  isAdmin?: boolean;
}

export function BackendCard({ 
  backend, 
  onEdit, 
  onRemove, 
  onBackup, 
  onRestore, 
  onVacuum, 
  onRestart,
  onShutdown,
  onShowInsights,
  isAdmin 
}: BackendCardProps) {
  const { colorScheme } = useMantineColorScheme();
  const healthy = backend.healthy;
  const latency = Number(backend.latencyMs);

  return (
    <Card 
      style={{ 
        borderTop: healthy 
          ? `${rem(3)} solid var(--mantine-color-successGreen-6)` 
          : `${rem(3)} solid var(--mantine-color-red-6)`
      }}
    >
      <Group justify="space-between" mb="md">
        <Group gap="sm">
          <ThemeIcon 
            variant="light" 
            color={healthy ? "successGreen" : "red"} 
            size="lg" 
          >
            <IconDatabase size={20} stroke={2} />
          </ThemeIcon>
          <Box style={{ flex: 1, minWidth: 0 }}>
            <Text fw={700} size="md" truncate="end">{backend.address}</Text>
            <Group gap={6} wrap="nowrap" mt={2}>
               <Box 
                 w={6} 
                 h={6} 
                 bg={healthy ? "successGreen.6" : "red.6"} 
                 style={{ borderRadius: '50%' }} 
               />
               <Text size="xs" c="dimmed" fw={600}>{healthy ? "Operational" : "Offline"}</Text>
               {backend.installedVersion && (
                 <>
                   <Text size="xs" c="dimmed" fw={700}>•</Text>
                   <Text size="xs" c="dimmed" fw={600}>v{backend.installedVersion}</Text>
                 </>
               )}
            </Group>
          </Box>
        </Group>
        
        {isAdmin && (
          <Menu shadow="md" width={200} position="bottom-end" radius="md">
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray">
                <IconDotsVertical size={16} stroke={2} />
              </ActionIcon>
            </Menu.Target>

            <Menu.Dropdown>
              <Menu.Label>Management</Menu.Label>
              <Menu.Item 
                leftSection={<IconEdit size={14} />}
                onClick={() => onEdit?.(backend)}
              >
                Edit Configuration
              </Menu.Item>

              {backend.managedByAgent && (
                <>
                  <Menu.Item 
                    leftSection={<IconDownload size={14} />}
                    onClick={() => onBackup?.(backend.address)}
                  >
                    Backup
                  </Menu.Item>
                  <Menu.Item 
                    leftSection={<IconUpload size={14} />}
                    onClick={() => onRestore?.(backend.address)}
                  >
                    Restore
                  </Menu.Item>
                  <Menu.Item 
                    leftSection={<IconWand size={14} />}
                    onClick={() => onVacuum?.(backend.address)}
                  >
                    Vacuum
                  </Menu.Item>
                  <Menu.Divider />
                  <Menu.Item 
                    leftSection={<IconSearch size={14} />}
                    onClick={() => onShowInsights?.(backend.address)}
                  >
                    View Insights
                  </Menu.Item>
                  <Menu.Item 
                    leftSection={<IconRefresh size={14} />}
                    onClick={() => onRestart?.(backend.address)}
                  >
                    Restart Service
                  </Menu.Item>
                  <Menu.Item 
                    leftSection={<IconPower size={14} />}
                    onClick={() => onShutdown?.(backend.address)}
                  >
                    Shutdown DB
                  </Menu.Item>
                </>
              )}

              <Menu.Divider />
              <Menu.Item
                color="red"
                leftSection={<IconTrash size={14} />}
                onClick={() => onRemove?.(backend.address)}
              >
                Remove Backend
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>

      <Stack gap="md">
        <Group justify="space-between" align="center">
          <Group gap={6}>
            <Badge 
              variant="light" 
              color={backend.role === "primary" ? "pontusBlue" : "indigo"}
              size="sm"
            >
              {backend.role}
            </Badge>
            {backend.recommendedVersion && backend.recommendedVersion !== backend.installedVersion && (
              <Badge 
                variant="filled" 
                color="blue" 
                size="sm"
                leftSection={<IconArrowUpCircle size={12} />}
                onClick={() => onEdit?.(backend)}
                style={{ cursor: 'pointer' }}
              >
                Update
              </Badge>
            )}
            {backend.managedByAgent && (
              <Badge 
                variant="outline" 
                color="gray" 
                size="sm"
                leftSection={<IconSettingsAutomation size={12} />}
              >
                Managed
              </Badge>
            )}
          </Group>
          <Group gap={6}>
            <Badge variant="default" color="gray" size="sm">
              W: {backend.weight}
            </Badge>
            {backend.isDraining && (
              <Badge variant="dot" color="orange" size="sm">
                Draining
              </Badge>
            )}
          </Group>
        </Group>

        <SimpleGrid cols={2} spacing="sm">
           <Paper p="sm" radius="md" withBorder bg={colorScheme === 'dark' ? 'var(--mantine-color-dark-8)' : 'var(--mantine-color-gray-0)'}>
              <Text size="xs" fw={600} c="dimmed" mb={4}>Latency</Text>
              <Group justify="space-between" align="center">
                 <Text fw={700} size="lg" c={latency > 100 ? "orange" : "successGreen"}>{latency}ms</Text>
                 <IconActivity size={16} color="var(--mantine-color-dimmed)" />
              </Group>
           </Paper>
           <Paper p="sm" radius="md" withBorder bg={colorScheme === 'dark' ? 'var(--mantine-color-dark-8)' : 'var(--mantine-color-gray-0)'}>
              <Text size="xs" fw={600} c="dimmed" mb={4}>RTT</Text>
              <Group justify="space-between" align="center">
                 <Text fw={700} size="lg" c={Number(backend.rttMs) > 50 ? "orange" : "blue"}>{backend.rttMs}ms</Text>
                 <IconNetwork size={16} color="var(--mantine-color-dimmed)" />
              </Group>
           </Paper>
        </SimpleGrid>

        <Box>
          <Group justify="space-between" mb={4}>
             <Text size="xs" fw={600} c="dimmed">Pool Capacity</Text>
             <Text size="xs" fw={700}>{Math.round((Number(backend.activeConns) / Number(backend.currentMaxConns)) * 100)}%</Text>
          </Group>
          <Progress 
              value={(Number(backend.activeConns) / Number(backend.currentMaxConns)) * 100} 
              color="pontusBlue" 
              size="sm" 
              radius="xl"
          />
          <Text size="xs" c="dimmed" mt={4} fw={500}>
             {backend.activeConns.toString()} / {backend.currentMaxConns} sessions
          </Text>
        </Box>

        {backend.dbMetrics && (
          <Stack gap="xs">
            <Divider variant="dashed" />
            <SimpleGrid cols={2} spacing="sm">
              <Box>
                <Text size="xs" fw={600} c="dimmed" mb={4}>DB Load</Text>
                <Group gap={4} align="baseline">
                  <Text fw={700}>{backend.dbMetrics.activeBackends.toString()}</Text>
                  <Text size="xs" c="dimmed">/ {backend.dbMetrics.maxBackends.toString()}</Text>
                </Group>
                <Progress 
                   value={(Number(backend.dbMetrics.activeBackends) / Number(backend.dbMetrics.maxBackends)) * 100} 
                   size={2} 
                   mt={4} 
                   color="indigo" 
                />
              </Box>
              <Box>
                <Text size="xs" fw={600} c="dimmed" mb={4}>Cache Hit</Text>
                <Text fw={700} c={backend.dbMetrics.cacheHitRatio < 0.9 ? "orange" : "successGreen"}>
                  {(backend.dbMetrics.cacheHitRatio * 100).toFixed(1)}%
                </Text>
                <Progress 
                   value={backend.dbMetrics.cacheHitRatio * 100} 
                   size={2} 
                   mt={4} 
                   color={backend.dbMetrics.cacheHitRatio < 0.9 ? "orange" : "successGreen"} 
                />
              </Box>
            </SimpleGrid>
          </Stack>
        )}
        
        {backend.lastCheck && (
          <Group gap={4} mt="xs" justify="center" style={{ opacity: 0.5 }}>
            <IconClock size={12} />
            <Text size="xs" fw={500}>
               Verified {new Date(Number(backend.lastCheck.seconds) * 1000).toLocaleTimeString()}
            </Text>
          </Group>
        )}
      </Stack>
    </Card>
  );
}
