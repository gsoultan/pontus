import { createTheme, rem, type MantineColorsTuple } from '@mantine/core';

const pontusBlue: MantineColorsTuple = [
  '#edf2ff',
  '#dbe4ff',
  '#bac8ff',
  '#91a7ff',
  '#748ffc',
  '#5c7cfa',
  '#4c6ef5',
  '#4263eb',
  '#3b5bdb',
  '#364fc7',
];

const successGreen: MantineColorsTuple = [
  '#ebfbee',
  '#d3f9d8',
  '#b2f2bb',
  '#8ce99a',
  '#69db7c',
  '#51cf66',
  '#40c057',
  '#37b24d',
  '#2f9e44',
  '#2b8a3e',
];

export const theme = createTheme({
  primaryColor: 'pontusBlue',
  primaryShade: { light: 7, dark: 6 },
  colors: {
    pontusBlue,
    successGreen,
    dark: [
      '#C1C2C5',
      '#A6A7AB',
      '#909296',
      '#5C5F66',
      '#373A40',
      '#2C2E33',
      '#25262B',
      '#1A1B1E',
      '#141517',
      '#101113',
    ],
  },
  defaultRadius: 'md',
  white: '#ffffff',
  black: '#0A0A0B',
  fontFamily: '"Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
  fontSizes: {
    xs: rem(12),
    sm: rem(14),
    md: rem(16),
    lg: rem(18),
    xl: rem(20),
  },
  spacing: {
    xs: rem(8),
    sm: rem(16),
    md: rem(24),
    lg: rem(32),
    xl: rem(40),
  },
  headings: {
    fontWeight: '700',
    fontFamily: '"Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
    sizes: {
      h1: { fontSize: rem(32), lineHeight: '1.2' },
      h2: { fontSize: rem(24), lineHeight: '1.3' },
      h3: { fontSize: rem(20), lineHeight: '1.4' },
    }
  },
  shadows: {
    xs: '0 1px 2px rgba(0,0,0,0.05)',
    sm: '0 1px 3px rgba(0,0,0,0.1), 0 1px 2px rgba(0,0,0,0.06)',
    md: '0 4px 6px -1px rgba(0,0,0,0.1), 0 2px 4px -1px rgba(0,0,0,0.06)',
    lg: '0 10px 15px -3px rgba(0,0,0,0.1), 0 4px 6px -2px rgba(0,0,0,0.05)',
    xl: '0 20px 25px -5px rgba(0,0,0,0.1), 0 10px 10px -5px rgba(0,0,0,0.04)',
    glass: '0 8px 32px 0 rgba(31, 38, 135, 0.07)',
  },
  components: {
    Card: {
      defaultProps: {
        withBorder: true,
        shadow: 'sm',
        padding: 'md',
        radius: 'md',
      },
      styles: {
        root: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))',
          transition: 'transform 150ms ease, box-shadow 150ms ease',
          borderColor: 'light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))',
          '&:hover': {
            transform: 'translateY(-2px)',
            boxShadow: 'var(--mantine-shadow-md)',
          },
        },
      },
    },
    Button: {
      defaultProps: {
        radius: 'md',
        fw: 600,
      },
      styles: {
        root: {
          transition: 'transform 100ms ease, opacity 100ms ease',
          '&:active': {
            transform: 'scale(0.98)',
          },
        },
      },
    },
    ActionIcon: {
      defaultProps: {
        radius: 'md',
        variant: 'light',
      },
      styles: {
        root: {
          transition: 'transform 100ms ease',
          '&:active': {
            transform: 'scale(0.92)',
          },
        },
      },
    },
    Paper: {
      defaultProps: {
        withBorder: true,
        shadow: 'xs',
        radius: 'md',
      },
    },
    Table: {
      defaultProps: {
        verticalSpacing: 'sm',
        horizontalSpacing: 'md',
        highlightOnHover: true,
      },
      styles: {
        thead: {
          backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))',
        },
        th: {
          textTransform: 'uppercase',
          fontSize: rem(12),
          fontWeight: 700,
          color: 'var(--mantine-color-dimmed)',
          borderBottom: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))`,
        },
      },
    },
    TextInput: {
      defaultProps: {
        radius: 'md',
        size: 'md',
      },
    },
    PasswordInput: {
      defaultProps: {
        radius: 'md',
        size: 'md',
      },
    },
    Select: {
      defaultProps: {
        radius: 'md',
        size: 'md',
      },
    },
    Modal: {
      defaultProps: {
        radius: 'lg',
        overlayProps: {
          blur: 3,
          backgroundOpacity: 0.4,
        },
      },
    },
    AppShell: {
      styles: {
        main: {
          backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-9))',
        },
      },
    },
  },
  other: {
    navbarWidth: rem(280),
    headerHeight: rem(72),
  },
});
