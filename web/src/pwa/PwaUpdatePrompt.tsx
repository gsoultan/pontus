import { useEffect } from 'react'
import { Button, Group, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCloudDownload, IconWifiOff } from '@tabler/icons-react'
import { useRegisterSW } from 'virtual:pwa-register/react'

const UPDATE_ID = 'pwa-update'
const OFFLINE_ID = 'pwa-offline'

/**
 * Surfaces service-worker lifecycle events.
 *
 * The worker is registered with `skipWaiting: false` deliberately: swapping the
 * bundle underneath an operator mid-failover would be hostile. The new version
 * installs quietly and waits until the reload is asked for.
 */
export function PwaUpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    offlineReady: [offlineReady, setOfflineReady],
    updateServiceWorker,
  } = useRegisterSW()

  useEffect(() => {
    if (!needRefresh) return
    notifications.show({
      id: UPDATE_ID,
      title: 'Update available',
      color: 'pontusBlue',
      icon: <IconCloudDownload size={18} />,
      autoClose: false,
      withCloseButton: true,
      onClose: () => setNeedRefresh(false),
      message: (
        <Group gap="sm" mt="xs">
          <Text size="sm">A newer dashboard build is ready.</Text>
          <Button size="compact-sm" onClick={() => void updateServiceWorker(true)}>
            Reload
          </Button>
        </Group>
      ),
    })
  }, [needRefresh, setNeedRefresh, updateServiceWorker])

  useEffect(() => {
    if (!offlineReady) return
    notifications.show({
      id: OFFLINE_ID,
      title: 'Ready to work offline',
      message: 'The dashboard shell is cached. Live data still needs a connection.',
      color: 'gray',
      icon: <IconWifiOff size={18} />,
      autoClose: 4000,
      onClose: () => setOfflineReady(false),
    })
  }, [offlineReady, setOfflineReady])

  return null
}
