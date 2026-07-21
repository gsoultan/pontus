import { 
  AppShell, 
  Burger, 
  Group, 
  Stack, 
  rem, 
  useMantineColorScheme, 
  ActionIcon, 
  Text,
  Box,
  useMantineTheme,
  Badge,
  ScrollArea,
  Divider,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { 
  IconLayoutDashboard, 
  IconSettings, 
  IconSun, 
  IconMoon,
  IconDatabase,
  IconActivity,
  IconTerminal,
  IconFolder,
  IconAdjustmentsHorizontal,
  IconLogout
} from '@tabler/icons-react';
import { Link, Outlet, useLocation } from '@tanstack/react-router';
import { ProjectSelector } from '../projects/ProjectSelector';
import { NavLinkItem } from './components/NavLinkItem';
import { useMemo } from 'react';
import { useAuthStore } from '../store/useAuthStore';
import { useServerInfo } from '../services/useServerInfo';


const navGroups = [
  {
    label: 'Dashboard',
    links: [
      { icon: IconLayoutDashboard, label: 'Overview', to: '/' },
    ],
  },
  {
    label: 'Management',
    links: [
      { icon: IconFolder, label: 'Projects', to: '/projects' },
    ],
  },
  {
    label: 'Observability',
    links: [
      { icon: IconActivity, label: 'Metrics', to: '/metrics' },
      { icon: IconDatabase, label: 'Query Analysis', to: '/queries' },
      { icon: IconAdjustmentsHorizontal, label: 'Tuning Advisor', to: '/tuning' },
      { icon: IconTerminal, label: 'System Logs', to: '/logs' },
    ],
  },
  {
    label: 'Settings',
    links: [
      { icon: IconSettings, label: 'System Settings', to: '/settings' },
    ],
  },
];

export function MainLayout() {
  const [opened, { toggle }] = useDisclosure(true);
  const location = useLocation();
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const theme = useMantineTheme();
  const logout = useAuthStore((state) => state.logout);
  const { data: serverInfo } = useServerInfo();

  const groups = useMemo(() => navGroups.map((group) => (
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
            isActive={location.pathname === link.to}
            onClick={() => {
              if (window.innerWidth < 768) {
                toggle();
              }
            }}
          />
        ))}
      </Stack>
    </Box>
  )), [location.pathname, theme.spacing.sm, toggle]);

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
            <Burger opened={opened} onClick={toggle} size="sm" color="gray.6" />
            <Link to="/" style={{ display: 'flex', alignItems: 'center', gap: rem(10), textDecoration: 'none', color: 'inherit' }}>
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
              <Text fw={800} size="lg" style={{ letterSpacing: rem(-0.5), color: 'light-dark(var(--mantine-color-pontusBlue-9), var(--mantine-color-white))' }}>
                PONTUS
              </Text>
            </Link>
          </Group>
          <Group gap="xs" wrap="nowrap">
            <ProjectSelector />
            <Divider orientation="vertical" h={20} mx="xs" visibleFrom="md" />
            <Badge 
              variant="dot" 
              color="successGreen" 
              size="sm" 
              visibleFrom="md"
            >
              Online
            </Badge>
            <ActionIcon
              variant="default"
              onClick={() => toggleColorScheme()}
              title="Toggle color scheme"
              size="lg"
            >
              {colorScheme === 'dark' ? <IconSun size={18} stroke={1.5} /> : <IconMoon size={18} stroke={1.5} />}
            </ActionIcon>
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
          <Box pt="sm">
            {groups}
          </Box>
        </AppShell.Section>
        
        <AppShell.Section pt="sm" style={{ borderTop: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))` }}>
          <Stack gap={4}>
            <Group px="sm" py="xs" wrap="nowrap">
               <Box 
                 bg="light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-dark-7))" 
                 p={rem(6)} 
                 style={{ borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
               >
                 <IconActivity size={14} color="var(--mantine-color-pontusBlue-6)" />
               </Box>
               <Box style={{ flex: 1 }}>
                 <Text size="xs" fw={600} truncate>{serverInfo?.version || 'v1.0.0'}</Text>
                 <Text size="10px" c="dimmed" fw={500} truncate>Cluster Healthy</Text>
               </Box>
               <ActionIcon 
                 variant="subtle" 
                 color="gray" 
                 onClick={() => logout()}
                 title="Logout"
               >
                 <IconLogout size={14} />
               </ActionIcon>
            </Group>
          </Stack>
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
