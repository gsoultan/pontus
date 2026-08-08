import { Code, ConnectError, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { notifications } from "@mantine/notifications";
import { useAuthStore } from "../store/useAuthStore";

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = useAuthStore.getState().token;
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }
  return await next(req);
};

/**
 * Guards against a notification per in-flight request. A dashboard has several
 * queries running at once, and every one of them fails together when a session
 * ends — the operator should be told once.
 */
let endingSession = false;

/** Ends the session and sends the operator back to sign in. */
function endSession(reason: string) {
  if (endingSession || !useAuthStore.getState().isAuthenticated) return;
  endingSession = true;

  notifications.show({
    id: "session-ended",
    title: "Session ended",
    message: reason,
    color: "orange",
    autoClose: 6000,
  });

  // The root route watches isAuthenticated and redirects to /login.
  useAuthStore.getState().logout();

  // Allow a later session to report its own ending.
  setTimeout(() => {
    endingSession = false;
  }, 1000);
}

/**
 * Ends the session when the server rejects the credentials.
 *
 * Without this a stored token that the server no longer accepts left the
 * dashboard permanently broken: isAuthenticated is persisted, so the router
 * admitted the operator, and every request then failed with "invalid token"
 * and no route back to the login screen short of clearing site data by hand.
 *
 * That is not hypothetical — switching the token format from JWT to PASETO
 * invalidated every token already in a browser, and rotating the signing key
 * does the same.
 *
 * PermissionDenied is deliberately not treated this way: the session is valid,
 * the role simply lacks the right, and signing out would hide that.
 */
const sessionInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (error) {
    if (ConnectError.from(error).code === Code.Unauthenticated) {
      endSession("Your sign-in is no longer valid. Please sign in again.");
    }
    throw error;
  }
};

export const transport = createConnectTransport({
  baseUrl: typeof window !== 'undefined' ? window.location.origin : "",
  // Order matters: the session guard wraps the auth header, so it sees the
  // rejection of the token that was actually attached.
  interceptors: [sessionInterceptor, authInterceptor],
});
