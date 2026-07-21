import { NavLink, rem, useMantineTheme } from '@mantine/core';
import { Link } from '@tanstack/react-router';
import { memo } from 'react';
import type { TablerIcon } from '@tabler/icons-react';

interface NavLinkItemProps {
  label: string;
  to: string;
  icon: TablerIcon;
  isActive: boolean;
  onClick?: () => void;
}

export const NavLinkItem = memo(({ label, to, icon: Icon, isActive, onClick }: NavLinkItemProps) => {
  const theme = useMantineTheme();

  return (
    <NavLink
      component={Link}
      to={to}
      label={label}
      leftSection={<Icon size={18} stroke={isActive ? 2 : 1.5} />}
      active={isActive}
      variant="filled"
      onClick={onClick}
      styles={{
        root: {
          borderRadius: theme.radius.md,
          fontWeight: isActive ? 600 : 500,
          height: rem(38),
          transition: 'all 150ms ease',
          backgroundColor: isActive 
            ? 'light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-pontusBlue-9))' 
            : 'transparent',
          color: isActive 
            ? 'light-dark(var(--mantine-color-pontusBlue-7), var(--mantine-color-white))' 
            : 'light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))',
          position: 'relative',
          '&:hover': {
            backgroundColor: isActive 
              ? 'light-dark(var(--mantine-color-pontusBlue-1), var(--mantine-color-pontusBlue-8))' 
              : 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-6))',
          },
        },
        label: {
          fontSize: rem(14),
        }
      }}
    />
  );
});

NavLinkItem.displayName = 'NavLinkItem';
