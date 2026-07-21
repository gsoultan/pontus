import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";
import type { GetServerInfoResponse } from "../gen/api/proto/endpoints/management_pb";

export function useServerInfo() {
  return useQuery<GetServerInfoResponse>({
    queryKey: ["serverInfo"],
    queryFn: async () => {
      return await statusClient.getServerInfo({});
    },
    staleTime: Infinity, // Server info doesn't change often
  });
}
