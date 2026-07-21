import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";

export function useDeleteProject() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: async (id) => {
      await statusClient.deleteProject({ id });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}
