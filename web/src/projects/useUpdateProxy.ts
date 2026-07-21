import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";
import type { 
  UpdateProxyRequest,
  UpdateProxyResponse
} from "../gen/api/proto/endpoints/management_pb";

export function useUpdateProxy() {
  const queryClient = useQueryClient();
  return useMutation<UpdateProxyResponse, Error, UpdateProxyRequest>({
    mutationFn: async (req) => {
      return await statusClient.updateProxy(req);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}
