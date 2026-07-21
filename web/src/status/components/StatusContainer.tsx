import { 
  Container, 
  SimpleGrid, 
  Center, 
  Text, 
  Stack, 
  Button, 
  ThemeIcon, 
  Box,
  Skeleton,
  Modal,
} from "@mantine/core";
import { 
  IconAlertCircle,
} from "@tabler/icons-react";
import { lazy, Suspense } from 'react';
import { BackendModal } from "./BackendModal";
import { TopQueries } from "./TopQueries";
import { SystemMetrics } from "./SystemMetrics";
import { AdvancedMetrics } from "./AdvancedMetrics";
import { SecurityCard } from "./SecurityCard";
import { ClusterOverview } from "./ClusterOverview";
import { AdaptiveStatusCard } from "./AdaptiveStatusCard";
import { PerformanceAdvisor } from "./PerformanceAdvisor";
import { PageHeader } from "../../layout/components/PageHeader";
import { StatusStats } from "./StatusStats";
import { StatusBreadcrumbs } from "./StatusBreadcrumbs";
import { StatusHeaderActions } from "./StatusHeaderActions";
import { BackendsSection } from "./BackendsSection";
import { StatusLoading } from "./StatusLoading";
import { MaintenanceModal } from "./MaintenanceModal";
import { PostgresInsights } from "./PostgresInsights";
import { useStatusContainerLogic } from "../hooks/useStatusContainerLogic";
import { useAuthStore } from "../../store/useAuthStore";

const PerformanceCharts = lazy(() => import("./PerformanceCharts").then(m => ({ default: m.PerformanceCharts })));
const HistoricalPerformanceCharts = lazy(() => import("./HistoricalPerformanceCharts").then(m => ({ default: m.HistoricalPerformanceCharts })));
const HistoricalTopQueries = lazy(() => import("./HistoricalTopQueries").then(m => ({ default: m.HistoricalTopQueries })));
const TopologyGraph = lazy(() => import("./TopologyGraph").then(m => ({ default: m.TopologyGraph })));
const SQLPlayground = lazy(() => import("./SQLPlayground").then(m => ({ default: m.SQLPlayground })));
const TuningAdvisor = lazy(() => import("./TuningAdvisor").then(m => ({ default: m.TuningAdvisor })));

export interface StatusContainerProps {
  view?: 'dashboard' | 'backends' | 'queries' | 'metrics' | 'tuning' | 'all';
}

export function StatusContainer({ view = 'all' }: StatusContainerProps) {
  const isAdmin = useAuthStore(state => state.role === 'admin');
  const {
    data,
    isLoading,
    error,
    context,
    opened,
    setOpened,
    editingBackend,
    setEditingBackend,
    initialFormValues,
    setInitialFormValues,
    handleAdd,
    openAddModal,
    handleUpdate,
    handleEdit,
    handleRemove,
    handleBackup,
    handleRestore,
    handleVacuum,
    handleInstallNode,
    isAdding,
    isUpdating,
    maintenanceOpened,
    setMaintenanceOpened,
    maintenanceType,
    targetAddress,
    backupBackend,
    restoreBackend,
    vacuumBackend,
    isBackingUp,
    isRestoring,
    isVacuuming,
    handleRestart,
    handleShutdown,
    handleShowInsights,
    insightsOpened,
    setInsightsOpened,
    insightsAddress,
    selectedProjectId,
    selectedProxyId
  } = useStatusContainerLogic();

  if (isLoading) return <StatusLoading />;

  if (error) {
    return (
      <Center h={400}>
        <Stack align="center">
          <ThemeIcon size={64} radius="xl" color="red" variant="light">
            <IconAlertCircle size={36} />
          </ThemeIcon>
          <Text fw={800} size="xl">System Unavailable</Text>
          <Text c="dimmed" ta="center" style={{ maxWidth: 400 }}>{error.message}</Text>
          <Button variant="light" color="red" onClick={() => window.location.reload()} mt="md">
            Retry Connection
          </Button>
        </Stack>
      </Center>
    );
  }

  const getTitle = () => {
    const titles: Record<string, string> = {
      backends: 'Backend Management',
      queries: 'Top Queries',
      metrics: 'Performance Metrics',
      tuning: 'Tuning Advisor',
      dashboard: 'System Dashboard',
      all: 'System Overview'
    };
    const base = titles[view] || titles.all;
    return context.proxy ? `${base}: ${context.proxy.name}` : base;
  };

  const getDescription = () => {
    switch (view) {
      case 'backends': return `Manage and monitor database nodes for ${context.proxy?.name || 'your proxy'}`;
      case 'queries': return 'Analyze the most frequent and expensive SQL queries processed by the proxy';
      case 'metrics': return 'Detailed throughput, latency, and error rate monitoring';
      case 'tuning': return 'Hardware-aware configuration optimization for PostgreSQL';
      default: return `Real-time status of your database proxy infrastructure.`;
    }
  };

  return (
    <Container size="xl" py="md">
      <Box mb="md">
        <StatusBreadcrumbs 
          projectName={context.project?.name} 
          proxyName={context.proxy?.name} 
        />
        <PageHeader 
          title={getTitle()} 
          description={getDescription()} 
          actions={
            <StatusHeaderActions 
              inFailover={data?.inFailover ?? false} 
              leaderId={data?.leaderId ?? "None"} 
              protocol={data?.protocol ?? "Unknown"} 
            />
          }
        />
      </Box>

      {(view === 'all' || view === 'dashboard' || view === 'metrics') && (
        <Stack gap="lg">
          <StatusStats data={data} />

          {(view === 'all' || view === 'dashboard') && (
            <Stack gap="md">
              <Suspense fallback={<Skeleton height={300} radius="md" />}>
                <PerformanceCharts />
              </Suspense>
              <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
                <Suspense fallback={<Skeleton height={400} radius="md" />}>
                  <TopologyGraph topology={data?.topology} />
                </Suspense>
                <Suspense fallback={<Skeleton height={400} radius="md" />}>
                  <SQLPlayground />
                </Suspense>
              </SimpleGrid>
            </Stack>
          )}

          {view === 'metrics' && (
            <Box>
              <Suspense fallback={<Skeleton height={500} radius="md" />}>
                <HistoricalPerformanceCharts />
              </Suspense>
            </Box>
          )}

          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
             <ClusterOverview backends={data?.backends ?? []} protocol={data?.protocol ?? "Unknown"} />
             <SecurityCard stats={data?.firewallStats} />
          </SimpleGrid>

          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
            <AdaptiveStatusCard status={data?.adaptiveStatus} />
            <PerformanceAdvisor suggestions={data?.performanceSuggestions ?? []} />
          </SimpleGrid>

          <SystemMetrics metrics={data?.systemMetrics} />
          <AdvancedMetrics metrics={data?.systemMetrics} />
        </Stack>
      )}

      {(view === 'all' || view === 'tuning') && (
        <Box mt="lg">
           <Suspense fallback={<Skeleton height={400} radius="md" />}>
             <TuningAdvisor />
           </Suspense>
        </Box>
      )}

      {(view === 'all' || view === 'backends') && (
        <Box mt="lg">
          <BackendsSection 
            backends={data?.backends ?? []} 
            onAdd={openAddModal} 
            onEdit={handleEdit} 
            onRemove={handleRemove} 
            onBackup={handleBackup}
            onRestore={handleRestore}
            onVacuum={handleVacuum}
            onRestart={handleRestart}
            onShutdown={handleShutdown}
            onShowInsights={handleShowInsights}
            isAdmin={isAdmin}
          />
        </Box>
      )}

      {view === 'all' && <Box mt="lg"><TopQueries queries={data?.topQueries ?? []} /></Box>}
      {view === 'queries' && (
        <Suspense fallback={<Skeleton height={400} radius="md" />}>
          <HistoricalTopQueries />
        </Suspense>
      )}

      <BackendModal 
        opened={opened} 
        onClose={() => {
          setOpened(false);
          setInitialFormValues(undefined);
        }}
        onSubmit={handleAdd}
        initialValues={initialFormValues}
        title="Register New Backend Node"
        loading={isAdding}
        hasPrimary={data?.backends?.some(b => b.role === "primary")}
      />

      <BackendModal 
        opened={!!editingBackend} 
        onClose={() => setEditingBackend(null)}
        onSubmit={handleUpdate}
        initialValues={editingBackend ? { 
          address: editingBackend.address, 
          role: editingBackend.role,
          weight: editingBackend.weight,
          managedByAgent: editingBackend.managedByAgent,
          agentAddress: editingBackend.agentAddress,
          agentConfig: editingBackend.agentConfig 
        } : undefined}
        title="Edit Backend Configuration"
        loading={isUpdating}
        hasPrimary={data?.backends?.some(b => b.role === "primary")}
      />

      <MaintenanceModal 
        opened={maintenanceOpened}
        onClose={() => setMaintenanceOpened(false)}
        backends={data?.backends || []}
        type={maintenanceType}
        onBackup={backupBackend}
        onRestore={restoreBackend}
        onVacuum={vacuumBackend}
        loading={isBackingUp || isRestoring || isVacuuming}
        initialAddress={targetAddress}
      />

      <Modal 
        opened={insightsOpened} 
        onClose={() => setInsightsOpened(false)} 
        size="xl" 
        title={`Database Insights: ${insightsAddress}`}
        radius="md"
      >
        <PostgresInsights 
          projectId={selectedProjectId || ""} 
          proxyId={selectedProxyId || ""} 
          address={insightsAddress} 
        />
      </Modal>
    </Container>
  );
}
