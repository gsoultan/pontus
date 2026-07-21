import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { useAuthStore } from "../store/useAuthStore";

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = useAuthStore.getState().token;
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }
  return await next(req);
};

export const transport = createConnectTransport({
  baseUrl: typeof window !== 'undefined' ? window.location.origin : "",
  interceptors: [authInterceptor],
});
