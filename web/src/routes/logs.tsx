import { createFileRoute } from '@tanstack/react-router'
import { Tabs, Container, Box } from '@mantine/core'
import { IconBroadcast, IconHistory } from '@tabler/icons-react'
import { LogsContainer } from '../logs/components/LogsContainer'
import { HistoricalLogsContainer } from '../logs/components/HistoricalLogsContainer'
import { useState } from 'react'

export const Route = createFileRoute('/logs')({
  component: LogsPage
})

function LogsPage() {
  const [activeTab, setActiveTab] = useState<string | null>('live')

  return (
    <Box>
      <Container size="xl">
        <Tabs value={activeTab} onChange={setActiveTab} variant="outline" radius="md" mt="md">
          <Tabs.List>
            <Tabs.Tab value="live" leftSection={<IconBroadcast size={16} />}>
              Live Stream
            </Tabs.Tab>
            <Tabs.Tab value="historical" leftSection={<IconHistory size={16} />}>
              Historical
            </Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="live">
            <LogsContainer />
          </Tabs.Panel>

          <Tabs.Panel value="historical">
            <HistoricalLogsContainer />
          </Tabs.Panel>
        </Tabs>
      </Container>
    </Box>
  )
}
