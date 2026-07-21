import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { GetMetricsHistoryResponse } from "../../gen/api/proto/endpoints/management_pb";
import { useProjectStore } from "../../store/useProjectStore";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

interface UseMetricsHistoryOptions {
  rangeHours?: number;
}

export function useMetricsHistory(options: UseMetricsHistoryOptions = {}) {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const rangeHours = options.rangeHours || 24;

  return useQuery<GetMetricsHistoryResponse>({
    queryKey: ["metrics-history", selectedProjectId, rangeHours],
    enabled: !!selectedProjectId,
    queryFn: async () => {
      const now = new Date();
      const startTime = new Date(now.getTime() - rangeHours * 60 * 60 * 1000);
      
      return await statusClient.getMetricsHistory({
        startTime: timestampFromDate(startTime),
        endTime: timestampFromDate(now),
      });
    },
    refetchInterval: 60000, // Refresh every minute
  });
}
