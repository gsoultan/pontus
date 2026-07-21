import { useState, useMemo, useCallback } from "react";
import { useStatus } from "./useStatus";
import { useBackendManagement } from "./useBackendManagement";
import { useProjectStore } from "../../store/useProjectStore";
import { useProjects } from "../../projects/useProjects";
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb";
import type { BackendConfig } from "../../gen/api/proto/domain/project_pb";

export function useStatusContainerLogic() {
  const { data, isLoading, error } = useStatus();
  const { data: projectsData } = useProjects();
  const { selectedProjectId, selectedProxyId } = useProjectStore();
  const { 
    addBackend, 
    removeBackend, 
    updateBackend, 
    isAdding, 
    isUpdating,
    backupBackend,
    restoreBackend,
    vacuumBackend,
    isBackingUp,
    isRestoring,
    isVacuuming,
    restartBackend,
    shutdownBackend,
    isRestarting,
    isShuttingDown
  } = useBackendManagement();
  
  const [opened, setOpened] = useState(false);
  const [editingBackend, setEditingBackend] = useState<BackendStatus | null>(null);
  const [initialFormValues, setInitialFormValues] = useState<any | undefined>(undefined);
  const [maintenanceOpened, setMaintenanceOpened] = useState(false);
  const [maintenanceType, setMaintenanceType] = useState<'backup' | 'restore' | 'vacuum'>('backup');
  const [targetAddress, setTargetAddress] = useState<string>("");
  const [insightsOpened, setInsightsOpened] = useState(false);
  const [insightsAddress, setInsightsAddress] = useState<string>("");

  const context = useMemo(() => {
    const project = projectsData?.projects.find(p => p.id === selectedProjectId);
    const proxy = project?.proxies.find(p => p.id === selectedProxyId);
    return { project, proxy };
  }, [projectsData, selectedProjectId, selectedProxyId]);

  const handleAdd = useCallback(async (values: { address: string; role: string; weight: number }) => {
    try {
      await addBackend({ config: values as BackendConfig });
      setOpened(false);
      setInitialFormValues(undefined);
    } catch {
      // Error handled in hook
    }
  }, [addBackend]);

  const openAddModal = useCallback((role: string = 'primary') => {
    setInitialFormValues({ address: '', role, weight: role === 'primary' ? 100 : 50 });
    setOpened(true);
  }, []);

  const handleUpdate = useCallback(async (values: { address: string; role: string; weight: number }) => {
    try {
      await updateBackend({ config: values as BackendConfig });
      setEditingBackend(null);
    } catch {
      // Error handled in hook
    }
  }, [updateBackend]);

  const handleEdit = useCallback((backend: BackendStatus) => {
    setEditingBackend(backend);
  }, []);

  const handleRemove = useCallback(async (address: string) => {
    if (window.confirm(`Are you sure you want to remove backend ${address}?`)) {
      await removeBackend({ address });
    }
  }, [removeBackend]);

  const handleBackup = useCallback(async (address: string) => {
    setTargetAddress(address);
    setMaintenanceType('backup');
    setMaintenanceOpened(true);
  }, []);

  const handleRestore = useCallback(async (address: string) => {
    setTargetAddress(address);
    setMaintenanceType('restore');
    setMaintenanceOpened(true);
  }, []);

  const handleVacuum = useCallback(async (address: string) => {
    setTargetAddress(address);
    setMaintenanceType('vacuum');
    setMaintenanceOpened(true);
  }, []);


  const handleRestart = useCallback(async (address: string) => {
    if (window.confirm(`Are you sure you want to restart the PostgreSQL service on ${address}?`)) {
      await restartBackend({ address });
    }
  }, [restartBackend]);

  const handleShutdown = useCallback(async (address: string) => {
    if (window.confirm(`Are you sure you want to shutdown the database on ${address}? This will stop all processing on this node.`)) {
      await shutdownBackend({ address });
    }
  }, [shutdownBackend]);

  const handleShowInsights = useCallback((address: string) => {
    setInsightsAddress(address);
    setInsightsOpened(true);
  }, []);

  return {
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
    isAdding,
    isUpdating,
    maintenanceOpened,
    setMaintenanceOpened,
    maintenanceType,
    setMaintenanceType,
    targetAddress,
    setTargetAddress,
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
    isRestarting,
    isShuttingDown,
    selectedProjectId,
    selectedProxyId
  };
}
