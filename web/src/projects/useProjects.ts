import { useQuery } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";
import type { 
  ListProjectsResponse
} from "../gen/api/proto/endpoints/management_pb";

export function useProjects() {
  return useQuery<ListProjectsResponse>({
    queryKey: ["projects"],
    queryFn: async () => {
      return await statusClient.listProjects({});
    },
  });
}
