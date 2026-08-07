import { createRootRoute, Outlet, useLocation, Navigate } from '@tanstack/react-router'
import { TanStackRouterDevtools } from '@tanstack/router-devtools'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MainLayout } from '../layout/MainLayout'
import { theme } from '../theme'
import { useAuthStore } from '../store/useAuthStore'
import { PwaUpdatePrompt } from '../pwa/PwaUpdatePrompt'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import '@mantine/charts/styles.css'

export const Route = createRootRoute({
  component: RootComponent,
})

function RootComponent() {
  const location = useLocation()
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated)

  if (!isAuthenticated && location.pathname !== '/login') {
    return <Navigate to="/login" />
  }

  return (
    <MantineProvider theme={theme} defaultColorScheme="dark">
      <Notifications position="bottom-right" limit={4} />
      <PwaUpdatePrompt />
      {location.pathname === '/login' ? <Outlet /> : <MainLayout />}
      {import.meta.env.DEV && <TanStackRouterDevtools />}
    </MantineProvider>
  )
}
