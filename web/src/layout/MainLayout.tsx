import {
  AppShell,
  Badge,
  Box,
  Burger,
  Divider,
  Group,
  ScrollArea,
  Stack,
  Text,
  ActionIcon,
  Tooltip,
  rem,
  useMantineColorScheme,
  useMantineTheme,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import {
  IconActivity,
  IconAdjustmentsHorizontal,
  IconDatabase,
  IconFolder,
  IconLayoutDashboard,
  IconLogout,
  IconMoon,
  IconServerBolt,
  IconSettings,
  IconSun,
  IconTerminal,
  IconTopologyStar3,
} from '@tabler/icons-react';
import { Link, Outlet, useLocation } from '@tanstack/react-router';
import { useMemo } from 'react';
import { ProjectSelector } from '../projects/ProjectSelector';
import { NavLinkItem } from './components/NavLinkItem';
import { useAuthStore } from '../store/useAuthStore';
import { useServerInfo } from '../services/useServerInfo';
import { useStatus } from '../status/hooks/useStatus';

const navGroups = [
  {
    label: 'Overview',
    links: [{ icon: IconLayoutDashboard, label: 'Dashboard', to: '/' }],
  },
  {
    label: 'Infrastructure',
    links: [
      { icon: IconFolder, label: 'Projects', to: '/projects' },
      { icon: IconDatabase, label: 'Backends', to: '/backends' },
      { icon: IconTopologyStar3, label: 'Cluster Operations', to: '/cluster' },
    ],
  },
  {
    label: 'Observability',
    links: [
      { icon: IconActivity, label: 'Metrics', to: '/metrics' },
      { icon: IconServerBolt, label: 'Query Analysis', to: '/queries' },
      { icon: IconAdjustmentsHorizontal, label: 'Tuning Advisor', to: '/tuning' },
      { icon: IconTerminal, label: 'System Logs', to: '/logs' },
    ],
  },
  {
    label: 'System',
    links: [{ icon: IconSettings, label: 'Settings', to: '/settings' }],
  },
];

/** `/` only matches exactly; every other entry also owns its child routes. */
function isRouteActive(pathname: string, to: string): boolean {
  return to === '/' ? pathname === '/' : pathname === to || pathname.startsWith(`${to}/`);
}

export function MainLayout() {
  const [opened, { toggle }] = useDisclosure(true);
  const location = useLocation();
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const theme = useMantineTheme();
  const logout = useAuthStore((state) => state.logout);
  const username = useAuthStore((state) => state.username);
  const role = useAuthStore((state) => state.role);
  const { data: serverInfo } = useServerInfo();
  const { data: status, isError } = useStatus();

  // Derived from real backend health rather than a hardcoded "Online" chip —
  // a status badge that cannot go red is worse than no badge at all.
  const health = useMemo(() => {
    if (isError) return { color: 'red', label: 'Unreachable' };
    if (!status) return { color: 'gray', label: 'No proxy selected' };
    if (status.inFailover) return { color: 'orange', label: 'Failover in progress' };
    const backends = status.backends ?? [];
    if (backends.length === 0) return { color: 'gray', label: 'No backends' };
    const down = backends.filter((backend) => !backend.healthy).length;
    if (down === backends.length) return { color: 'red', label: 'All backends down' };
    if (down > 0) return { color: 'orange', label: `${down} backend(s) down` };
    return { color: 'successGreen', label: 'Healthy' };
  }, [status, isError]);

  const groups = useMemo(
    () =>
      navGroups.map((group) => (
        <Box key={group.label} mb="lg">
          <Text
            size="xs"
            fw={600}
            c="dimmed"
            style={{
              letterSpacing: rem(0.5),
              marginBottom: rem(4),
              paddingLeft: theme.spacing.sm,
            }}
          >
            {group.label}
          </Text>
          <Stack gap={2}>
            {group.links.map((link) => (
              <NavLinkItem
                key={link.label}
                label={link.label}
                to={link.to}
                icon={link.icon}
                isActive={isRouteActive(location.pathname, link.to)}
                onClick={() => {
                  if (window.innerWidth < 768) toggle();
                }}
              />
            ))}
          </Stack>
        </Box>
      )),
    [location.pathname, theme.spacing.sm, toggle],
  );

  return (
    <AppShell
      header={{ height: 64 }}
      navbar={{
        width: 260,
        breakpoint: 'sm',
        collapsed: { mobile: !opened, desktop: false },
      }}
      padding="md"
    >
      <AppShell.Header
        style={{
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-8))',
          borderBottom: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))`,
          zIndex: 100,
        }}
      >
        <Group h="100%" px="md" justify="space-between">
          <Group gap="sm">
            <Burger opened={opened} onClick={toggle} size="sm" color="gray.6" aria-label="Toggle navigation" />
            <Link
              to="/"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: rem(10),
                textDecoration: 'none',
                color: 'inherit',
              }}
            >
              <Box
                bg="pontusBlue.6"
                p={rem(6)}
                style={{
                  borderRadius: rem(8),
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <IconDatabase size={18} color="white" stroke={2} />
              </Box>
              <Text
                fw={800}
                size="lg"
                style={{
                  letterSpacing: rem(-0.5),
                  color: 'light-dark(var(--mantine-color-pontusBlue-9), var(--mantine-color-white))',
                }}
              >
                PONTUS
              </Text>
            </Link>
          </Group>
          <Group gap="xs" wrap="nowrap">
            <ProjectSelector />
            <Divider orientation="vertical" h={20} mx="xs" visibleFrom="md" />
            <Badge variant="dot" color={health.color} size="sm" visibleFrom="md">
              {health.label}
            </Badge>
            <Tooltip label={colorScheme === 'dark' ? 'Switch to light' : 'Switch to dark'}>
              <ActionIcon
                variant="default"
                onClick={() => toggleColorScheme()}
                aria-label="Toggle color scheme"
                size="lg"
              >
                {colorScheme === 'dark' ? <IconSun size={18} stroke={1.5} /> : <IconMoon size={18} stroke={1.5} />}
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar
        p="sm"
        style={{
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-8))',
          borderRight: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))`,
        }}
      >
        <AppShell.Section grow component={ScrollArea} mx="-sm" px="sm">
          <Box pt="sm">{groups}</Box>
        </AppShell.Section>

        <AppShell.Section
          pt="sm"
          style={{
            borderTop: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))`,
          }}
        >
          <Group px="sm" py="xs" wrap="nowrap">
            <Box
              bg="light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-dark-7))"
              p={rem(6)}
              style={{
                borderRadius: '50%',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <IconActivity size={14} color="var(--mantine-color-pontusBlue-6)" />
            </Box>
            <Box style={{ flex: 1, minWidth: 0 }}>
              <Text size="xs" fw={600} truncate>
                {username || 'Signed in'}
              </Text>
              <Text size="10px" c="dimmed" fw={500} truncate>
                {role ? `${role} · ` : ''}
                {serverInfo?.version || 'unknown version'}
              </Text>
            </Box>
            <Tooltip label="Sign out">
              <ActionIcon variant="subtle" color="gray" onClick={() => logout()} aria-label="Sign out">
                <IconLogout size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>
        <Box style={{ maxWidth: rem(1440), margin: '0 auto' }}>
          <Outlet />
        </Box>
      </AppShell.Main>
    </AppShell>
  );
}
