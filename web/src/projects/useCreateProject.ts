import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";
import type { 
  CreateProjectRequest, 
  CreateProjectResponse
} from "../gen/api/proto/endpoints/management_pb";

export function useCreateProject() {
  const queryClient = useQueryClient();
  return useMutation<CreateProjectResponse, Error, CreateProjectRequest>({
    mutationFn: async (req) => {
      return await statusClient.createProject(req);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}
