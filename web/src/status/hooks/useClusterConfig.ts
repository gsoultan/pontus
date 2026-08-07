import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import { create } from "@bufbuild/protobuf";
import { SetClusterConfigRequestSchema } from "../../gen/api/proto/endpoints/management_pb";
import { notifications } from "@mantine/notifications";

export function useClusterConfig() {
  const queryClient = useQueryClient();

  const configQuery = useQuery({
    queryKey: ["clusterConfig"],
    queryFn: async () => {
      return await statusClient.getClusterConfig({});
    },
  });

  const setConfigMutation = useMutation({
    mutationFn: async (parameters: Record<string, string>) => {
      return await statusClient.setClusterConfig(
        create(SetClusterConfigRequestSchema, { parameters })
      );
    },
    onSuccess: (response) => {
      queryClient.invalidateQueries({ queryKey: ["clusterConfig"] });

      // A cluster-wide write can succeed on some nodes and fail on others.
      // Reporting only "success" would leave the operator believing the whole
      // cluster took the change when part of it silently did not.
      if (response.failedNodes.length > 0) {
        notifications.show({
          title: "Applied with failures",
          message: `Updated ${response.updatedNodes.length} node(s); failed on ${response.failedNodes.join(", ")}`,
          color: "orange",
          autoClose: false,
        });
        return;
      }

      notifications.show({
        title: "Success",
        message: `Configuration applied to ${response.updatedNodes.length || "all"} node(s)`,
        color: "green",
      });
    },
    onError: (error: Error) => {
      notifications.show({
        title: "Error",
        message: error.message || "Failed to update cluster configuration",
        color: "red",
      });
    },
  });

  return {
    config: configQuery.data?.parameters || {},
    isLoading: configQuery.isLoading,
    isUpdating: setConfigMutation.isPending,
    updateConfig: setConfigMutation.mutateAsync,
    lastResult: setConfigMutation.data,
    refetch: configQuery.refetch,
  };
}
