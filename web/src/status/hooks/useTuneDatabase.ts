import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { TuneDatabaseResponse } from "../../gen/api/proto/endpoints/management_pb";
import { useProjectStore } from "../../store/useProjectStore";

export function useTuneDatabase(address?: string) {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const selectedProxyId = useProjectStore(s => s.selectedProxyId);

  return useQuery<TuneDatabaseResponse>({
    queryKey: ["tune-database", selectedProjectId, selectedProxyId, address],
    enabled: !!selectedProjectId,
    queryFn: async () => {
      return await statusClient.tuneDatabase({ 
        projectId: selectedProjectId!,
        proxyId: selectedProxyId || undefined,
        address: address
      });
    },
  });
}
