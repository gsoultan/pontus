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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["clusterConfig"] });
      notifications.show({
        title: "Success",
        message: "Cluster configuration updated successfully",
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
    refetch: configQuery.refetch,
  };
}
