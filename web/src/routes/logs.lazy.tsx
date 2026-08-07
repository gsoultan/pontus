import { createLazyFileRoute } from '@tanstack/react-router'
import { Container, Tabs } from '@mantine/core'
import { IconBroadcast, IconHistory } from '@tabler/icons-react'
import { useState } from 'react'
import { PageHeader } from '../layout/components/PageHeader'
import { LogsContainer } from '../logs/components/LogsContainer'
import { HistoricalLogsContainer } from '../logs/components/HistoricalLogsContainer'

export const Route = createLazyFileRoute('/logs')({
  component: LogsPage,
})

function LogsPage() {
  const [activeTab, setActiveTab] = useState<string | null>('live')

  return (
    <Container size="xl" py="md">
      <PageHeader
        title="System Logs"
        description="Live proxy output and the searchable seven-day history"
      />

      <Tabs value={activeTab} onChange={setActiveTab} variant="outline" radius="md">
        <Tabs.List>
          <Tabs.Tab value="live" leftSection={<IconBroadcast size={16} />}>
            Live Stream
          </Tabs.Tab>
          <Tabs.Tab value="historical" leftSection={<IconHistory size={16} />}>
            Historical
          </Tabs.Tab>
        </Tabs.List>

        {/* Mounted lazily per tab so the live stream is torn down when hidden. */}
        <Tabs.Panel value="live">
          {activeTab === 'live' && <LogsContainer />}
        </Tabs.Panel>

        <Tabs.Panel value="historical">
          {activeTab === 'historical' && <HistoricalLogsContainer />}
        </Tabs.Panel>
      </Tabs>
    </Container>
  )
}
