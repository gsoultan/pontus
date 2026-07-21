import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { GetTopQueriesHistoryResponse } from "../../gen/api/proto/endpoints/management_pb";
import { useProjectStore } from "../../store/useProjectStore";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

interface UseTopQueriesHistoryOptions {
  rangeHours?: number;
  limit?: number;
}

export function useTopQueriesHistory(options: UseTopQueriesHistoryOptions = {}) {
  const selectedProjectId = useProjectStore(s => s.selectedProjectId);
  const rangeHours = options.rangeHours || 24;
  const limit = options.limit || 10;

  return useQuery<GetTopQueriesHistoryResponse>({
    queryKey: ["top-queries-history", selectedProjectId, rangeHours, limit],
    enabled: !!selectedProjectId,
    queryFn: async () => {
      const now = new Date();
      const startTime = new Date(now.getTime() - rangeHours * 60 * 60 * 1000);
      
      return await statusClient.getTopQueriesHistory({
        startTime: timestampFromDate(startTime),
        endTime: timestampFromDate(now),
        limit: limit,
      });
    },
    refetchInterval: 60000,
  });
}
