import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import { useProjectStore } from "../../store/useProjectStore";
import type { TuningSuggestion } from "../../gen/api/proto/domain/query_pb";
import { notifications } from "@mantine/notifications";

export function useApplyTuning() {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const selectedProxyId = useProjectStore(s => s.selectedProxyId);
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ suggestion, address }: { suggestion: TuningSuggestion; address?: string }) => {
      if (!selectedProjectId) throw new Error("No project selected");

      return await statusClient.applyTuning({
        projectId: selectedProjectId,
        proxyId: selectedProxyId || undefined,
        address: address,
        suggestion: suggestion,
      });
    },
    onSuccess: (data) => {
      if (data.success) {
        notifications.show({
          title: "Success",
          message: data.message,
          color: "green",
        });
        // Refresh tuning recommendations
        queryClient.invalidateQueries({ queryKey: ["tune-database"] });
      } else {
        notifications.show({
          title: "Failed to apply tuning",
          message: data.message,
          color: "red",
        });
      }
    },
    onError: (error: any) => {
      notifications.show({
        title: "Error",
        message: error.message || "An unexpected error occurred",
        color: "red",
      });
    },
  });
}
