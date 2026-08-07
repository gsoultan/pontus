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
  SimpleGrid,
  Tooltip,
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
  IconSearch,
  IconCloudUpload,
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
  onPromote?: (address: string) => void;
  onShowInsights?: (address: string) => void;
  isAdmin?: boolean;
}

/** Guards the zero-capacity case — a node with no pool would render "NaN%". */
function utilization(active: number, max: number): number | null {
  if (!Number.isFinite(max) || max <= 0) return null;
  return Math.min(100, (active / max) * 100);
}

function lagLabel(lagMs: number): { color: string; text: string } {
  if (lagMs <= 0) return { color: "successGreen", text: "In sync" };
  if (lagMs < 1000) return { color: "successGreen", text: `${lagMs}ms` };
  if (lagMs < 10_000) return { color: "orange", text: `${(lagMs / 1000).toFixed(1)}s` };
  return { color: "red", text: `${(lagMs / 1000).toFixed(0)}s` };
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
  onPromote,
  onShowInsights,
  isAdmin
}: BackendCardProps) {
  const healthy = backend.healthy;
  const latency = Number(backend.latencyMs);
  const isReplica = backend.role !== "primary";
  const poolUsage = utilization(Number(backend.activeConns), Number(backend.currentMaxConns));
  // active_conns includes streams; sessions are the remainder.
  const streamConns = Number(backend.streamConns ?? 0);
  const sessionConns = Math.max(0, Number(backend.activeConns) - streamConns);
  const sessionUsage = utilization(sessionConns, Number(backend.currentMaxConns));
  const streamUsage = utilization(streamConns, Number(backend.currentMaxConns));
  const lag = lagLabel(Number(backend.replicationLagMs));
  const dbUsage = backend.dbMetrics
    ? utilization(Number(backend.dbMetrics.activeBackends), Number(backend.dbMetrics.maxBackends))
    : null;

  return (
    <Card
      style={{
        borderTop: healthy
          ? `${rem(3)} solid var(--mantine-color-successGreen-6)`
          : `${rem(3)} solid var(--mantine-color-red-6)`
      }}
    >
      <Group justify="space-between" mb="md">
        <Group gap="sm" style={{ minWidth: 0, flex: 1 }}>
          <ThemeIcon variant="light" color={healthy ? "successGreen" : "red"} size="lg">
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
              {backend.zone && (
                <>
                  <Text size="xs" c="dimmed" fw={700}>•</Text>
                  <Text size="xs" c="dimmed" fw={600} truncate>{backend.zone}</Text>
                </>
              )}
            </Group>
          </Box>
        </Group>

        {isAdmin && (
          <Menu shadow="md" width={220} position="bottom-end" radius="md">
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${backend.address}`}>
                <IconDotsVertical size={16} stroke={2} />
              </ActionIcon>
            </Menu.Target>

            <Menu.Dropdown>
              <Menu.Label>Management</Menu.Label>
              <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => onEdit?.(backend)}>
                Edit Configuration
              </Menu.Item>

              {isReplica && (
                <Menu.Item
                  leftSection={<IconCloudUpload size={14} />}
                  onClick={() => onPromote?.(backend.address)}
                >
                  Promote to Primary
                </Menu.Item>
              )}

              {backend.managedByAgent && (
                <>
                  <Menu.Label>Maintenance</Menu.Label>
                  <Menu.Item
                    leftSection={<IconSearch size={14} />}
                    onClick={() => onShowInsights?.(backend.address)}
                  >
                    View Insights
                  </Menu.Item>
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
                </>
              )}

              <Menu.Divider />
              <Menu.Label c="red">Danger zone</Menu.Label>
              {backend.managedByAgent && (
                <>
                  <Menu.Item
                    color="orange"
                    leftSection={<IconRefresh size={14} />}
                    onClick={() => onRestart?.(backend.address)}
                  >
                    Restart Service
                  </Menu.Item>
                  <Menu.Item
                    color="red"
                    leftSection={<IconPower size={14} />}
                    onClick={() => onShutdown?.(backend.address)}
                  >
                    Shutdown Database
                  </Menu.Item>
                </>
              )}
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
              <Tooltip label={`Recommended: v${backend.recommendedVersion}`}>
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
              </Tooltip>
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

        <SimpleGrid cols={isReplica ? 3 : 2} spacing="sm">
          <Paper p="sm" radius="md" withBorder bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
            <Text size="xs" fw={600} c="dimmed" mb={4}>Latency</Text>
            <Group justify="space-between" align="center" wrap="nowrap">
              <Text fw={700} size="lg" c={latency > 100 ? "orange" : "successGreen"}>{latency}ms</Text>
              <IconActivity size={16} color="var(--mantine-color-dimmed)" />
            </Group>
          </Paper>
          <Paper p="sm" radius="md" withBorder bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
            <Text size="xs" fw={600} c="dimmed" mb={4}>RTT</Text>
            <Group justify="space-between" align="center" wrap="nowrap">
              <Text fw={700} size="lg" c={Number(backend.rttMs) > 50 ? "orange" : "blue"}>{backend.rttMs}ms</Text>
              <IconNetwork size={16} color="var(--mantine-color-dimmed)" />
            </Group>
          </Paper>
          {isReplica && (
            <Paper p="sm" radius="md" withBorder bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
              <Text size="xs" fw={600} c="dimmed" mb={4}>Repl. lag</Text>
              <Text fw={700} size="lg" c={lag.color}>{lag.text}</Text>
            </Paper>
          )}
        </SimpleGrid>

        <Box>
          <Group justify="space-between" mb={4}>
            <Text size="xs" fw={600} c="dimmed">Pool Capacity</Text>
            <Text size="xs" fw={700}>
              {poolUsage === null ? "—" : `${Math.round(poolUsage)}%`}
            </Text>
          </Group>
          {/* Sessions and streams are shown apart because they behave apart: a
              session returns to the pool after its transaction, a stream holds
              its slot for hours. One bar labelled "connections" would hide how
              much of this capacity is never coming back. */}
          <Progress.Root size="sm" radius="xl">
            <Progress.Section value={sessionUsage ?? 0} color="pontusBlue" />
            <Progress.Section value={streamUsage ?? 0} color="grape" />
          </Progress.Root>
          <Group gap={10} mt={4} wrap="wrap">
            <Text size="xs" c="dimmed" fw={500}>
              {poolUsage === null
                ? "No pool capacity reported"
                : `${sessionConns} sessions`}
            </Text>
            {streamConns > 0 && (
              <Group gap={4} wrap="nowrap">
                <Box w={6} h={6} bg="grape.5" style={{ borderRadius: '50%' }} />
                <Text size="xs" c="grape" fw={600}>
                  {streamConns} stream{streamConns === 1 ? '' : 's'} held
                </Text>
              </Group>
            )}
            {poolUsage !== null && (
              <Text size="xs" c="dimmed" fw={500}>
                {Math.max(0, Number(backend.currentMaxConns) - Number(backend.activeConns))} free
              </Text>
            )}
          </Group>
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
                <Progress value={dbUsage ?? 0} size={2} mt={4} color="indigo" />
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
