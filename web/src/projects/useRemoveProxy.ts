import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";

export function useRemoveProxy() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { projectId: string; proxyId: string }>({
    mutationFn: async (req) => {
      await statusClient.removeProxy(req);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}
