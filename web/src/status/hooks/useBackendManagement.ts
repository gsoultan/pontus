import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { BackendConfig } from "../../gen/api/proto/domain/project_pb";
import { notifications } from "@mantine/notifications";
import { useProjectStore } from "../../store/useProjectStore";

export function useBackendManagement() {
  const queryClient = useQueryClient();
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const selectedProxyId = useProjectStore(s => s.selectedProxyId);

  const addMutation = useMutation({
    mutationFn: async ({ config, projectId, proxyId }: { config: BackendConfig, projectId?: string, proxyId?: string }) => {
      const pId = projectId || selectedProjectId;
      const pxId = proxyId || selectedProxyId;
      if (!pId) throw new Error("No project selected");
      if (!pxId) throw new Error("No proxy selected");
      return await statusClient.addBackend({ 
        projectId: pId, 
        proxyId: pxId,
        config 
      });
    },
    onSuccess: (_, variables) => {
      const pId = variables.projectId || selectedProjectId;
      const pxId = variables.proxyId || selectedProxyId;
      queryClient.invalidateQueries({ queryKey: ["status", pId, pxId] });
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        title: "Success",
        message: "Backend added successfully",
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to add backend",
        color: "red",
      });
    },
  });

  const removeMutation = useMutation({
    mutationFn: async ({ address, projectId, proxyId }: { address: string, projectId?: string, proxyId?: string }) => {
      const pId = projectId || selectedProjectId;
      const pxId = proxyId || selectedProxyId;
      if (!pId) throw new Error("No project selected");
      if (!pxId) throw new Error("No proxy selected");
      return await statusClient.removeBackend({ 
        projectId: pId, 
        proxyId: pxId,
        address 
      });
    },
    onSuccess: (_, variables) => {
      const pId = variables.projectId || selectedProjectId;
      const pxId = variables.proxyId || selectedProxyId;
      queryClient.invalidateQueries({ queryKey: ["status", pId, pxId] });
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        title: "Success",
        message: "Backend removed successfully",
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to remove backend",
        color: "red",
      });
    },
  });

  const updateMutation = useMutation({
    mutationFn: async ({ config, projectId, proxyId, address }: { config: BackendConfig, projectId?: string, proxyId?: string, address?: string }) => {
      const pId = projectId || selectedProjectId;
      const pxId = proxyId || selectedProxyId;
      if (!pId) throw new Error("No project selected");
      if (!pxId) throw new Error("No proxy selected");
      return await statusClient.updateBackend({ 
        projectId: pId, 
        proxyId: pxId,
        address: address || config.address,
        config 
      });
    },
    onSuccess: (_, variables) => {
      const pId = variables.projectId || selectedProjectId;
      const pxId = variables.proxyId || selectedProxyId;
      queryClient.invalidateQueries({ queryKey: ["status", pId, pxId] });
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({
        title: "Success",
        message: "Backend updated successfully",
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to update backend",
        color: "red",
      });
    },
  });

  const validateMutation = useMutation({
    mutationFn: async (address: string) => {
      return await statusClient.validateBackend({ address });
    },
  });

  const getAgentInfoMutation = useMutation({
    mutationFn: async ({ agentAddress, agentToken }: { agentAddress: string, agentToken?: string }) => {
      return await statusClient.getAgentInfo({ agentAddress, agentToken });
    },
  });


  const backupMutation = useMutation({
    mutationFn: async ({ address, backupPath }: { address: string, backupPath: string }) => {
      const pId = selectedProjectId;
      const pxId = selectedProxyId;
      if (!pId || !pxId) throw new Error("Context missing");

      for await (const progress of statusClient.backupBackend({ projectId: pId, proxyId: pxId, address, backupPath })) {
        notifications.show({
          id: `backup-${address}`,
          title: "Backup Progress",
          message: `${progress.stage}: ${progress.message} (${progress.percentage}%)`,
          loading: progress.percentage < 100,
          autoClose: progress.percentage === 100 ? 5000 : false,
        });
      }
    },
  });

  const restoreMutation = useMutation({
    mutationFn: async ({ address, backupPath }: { address: string, backupPath: string }) => {
      const pId = selectedProjectId;
      const pxId = selectedProxyId;
      if (!pId || !pxId) throw new Error("Context missing");

      for await (const progress of statusClient.restoreBackend({ projectId: pId, proxyId: pxId, address, backupPath })) {
        notifications.show({
          id: `restore-${address}`,
          title: "Restore Progress",
          message: `${progress.stage}: ${progress.message} (${progress.percentage}%)`,
          loading: progress.percentage < 100,
          autoClose: progress.percentage === 100 ? 5000 : false,
        });
      }
    },
  });

  const vacuumMutation = useMutation({
    mutationFn: async ({ address, database, full }: { address: string, database: string, full: boolean }) => {
      const pId = selectedProjectId;
      const pxId = selectedProxyId;
      if (!pId || !pxId) throw new Error("Context missing");

      for await (const progress of statusClient.vacuumBackend({ projectId: pId, proxyId: pxId, address, database, full })) {
        notifications.show({
          id: `vacuum-${address}`,
          title: "Vacuum Progress",
          message: `${progress.stage}: ${progress.message} (${progress.percentage}%)`,
          loading: progress.percentage < 100,
          autoClose: progress.percentage === 100 ? 5000 : false,
        });
      }
    },
  });

  const restartMutation = useMutation({
    mutationFn: async ({ address, projectId, proxyId }: { address: string, projectId?: string, proxyId?: string }) => {
      const pId = projectId || selectedProjectId;
      const pxId = proxyId || selectedProxyId;
      if (!pId || !pxId) throw new Error("Context missing");
      return await statusClient.restartBackendService({ projectId: pId, proxyId: pxId, address });
    },
    onSuccess: () => {
      notifications.show({
        title: "Success",
        message: "Database service restart initiated",
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to restart service",
        color: "red",
      });
    },
  });

  const shutdownMutation = useMutation({
    mutationFn: async ({ address, projectId, proxyId }: { address: string, projectId?: string, proxyId?: string }) => {
      const pId = projectId || selectedProjectId;
      const pxId = proxyId || selectedProxyId;
      if (!pId || !pxId) throw new Error("Context missing");
      return await statusClient.shutdownBackend({ projectId: pId, proxyId: pxId, address });
    },
    onSuccess: () => {
      notifications.show({
        title: "Success",
        message: "Database shutdown initiated",
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to shutdown database",
        color: "red",
      });
    },
  });

  const availableVersionsQuery = useQuery({
    queryKey: ["availableVersions"],
    queryFn: async () => {
      const resp = await statusClient.getAvailableVersions({});
      return resp.versions;
    },
    staleTime: 1000 * 60 * 60, // 1 hour
  });

  return {
    addBackend: addMutation.mutateAsync,
    isAdding: addMutation.isPending,
    removeBackend: removeMutation.mutateAsync,
    isRemoving: removeMutation.isPending,
    updateBackend: updateMutation.mutateAsync,
    isUpdating: updateMutation.isPending,
    validateBackend: validateMutation.mutateAsync,
    isValidating: validateMutation.isPending,
    getAgentInfo: getAgentInfoMutation.mutateAsync,
    isGettingAgentInfo: getAgentInfoMutation.isPending,
    backupBackend: backupMutation.mutateAsync,
    isBackingUp: backupMutation.isPending,
    restoreBackend: restoreMutation.mutateAsync,
    isRestoring: restoreMutation.isPending,
    vacuumBackend: vacuumMutation.mutateAsync,
    isVacuuming: vacuumMutation.isPending,
    restartBackend: restartMutation.mutateAsync,
    isRestarting: restartMutation.isPending,
    shutdownBackend: shutdownMutation.mutateAsync,
    isShuttingDown: shutdownMutation.isPending,
    availableVersions: availableVersionsQuery.data || [],
    isLoadingAvailableVersions: availableVersionsQuery.isLoading,
  };
}
