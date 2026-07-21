import { useMutation, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../status/services/statusService";
import type { 
  AddProxyRequest,
  AddProxyResponse
} from "../gen/api/proto/endpoints/management_pb";

export function useAddProxy() {
  const queryClient = useQueryClient();
  return useMutation<AddProxyResponse, Error, AddProxyRequest>({
    mutationFn: async (req) => {
      return await statusClient.addProxy(req);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });
}
