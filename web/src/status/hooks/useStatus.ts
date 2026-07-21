import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { GetStatusResponse } from "../../gen/api/proto/endpoints/management_pb";
import { useProjectStore } from "../../store/useProjectStore";

export function useStatus() {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const selectedProxyId = useProjectStore(s => s.selectedProxyId);

  return useQuery<GetStatusResponse>({
    queryKey: ["status", selectedProjectId, selectedProxyId],
    enabled: !!selectedProjectId,
    queryFn: async () => {
      return await statusClient.getStatus({ 
        projectId: selectedProjectId!,
        proxyId: selectedProxyId || undefined 
      });
    },
    refetchInterval: 5000, // Poll every 5 seconds
  });
}
