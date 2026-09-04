import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { statusClient } from "../services/statusService";
import type { GetStatusResponse } from "../../gen/api/proto/endpoints/management_pb";
import { useProjectStore } from "../../store/useProjectStore";

/** Fallback poll cadence, used only when the stream is unavailable. */
const FALLBACK_POLL_MS = 5000;

/** How long to wait before retrying a dropped stream. */
const RECONNECT_MS = 3000;

/**
 * Subscribes to the proxy status.
 *
 * The dashboard used to poll GetStatus every five seconds, re-fetching every
 * backend, twenty top queries with their full SQL text, the topology and the
 * system metrics on each tick whether or not anything had changed. StreamStatus
 * lets the server push instead, and suppress payloads that are identical.
 *
 * The query cache stays the source of truth so every existing consumer and
 * every mutation's invalidateQueries keeps working unchanged; the stream just
 * writes into it. If the stream cannot be established the hook falls back to
 * polling, so an older server or a proxy that blocks streaming still works.
 */
export function useStatus() {
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId);
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId);
  const queryClient = useQueryClient();

  const [streaming, setStreaming] = useState(false);
  // Read inside the effect without making it a dependency, so a state flip
  // cannot tear down and re-open the stream it just reported on.

  const query = useQuery<GetStatusResponse>({
    queryKey: ["status", selectedProjectId, selectedProxyId],
    enabled: !!selectedProjectId,
    queryFn: async () =>
      await statusClient.getStatus({
        projectId: selectedProjectId!,
        proxyId: selectedProxyId || undefined,
      }),
    // While streaming, the socket is the refresh mechanism; polling would
    // reintroduce exactly the cost this replaces.
    refetchInterval: streaming ? false : FALLBACK_POLL_MS,
    staleTime: streaming ? Infinity : 0,
  });

  useEffect(() => {
    if (!selectedProjectId) return;

    const controller = new AbortController();
    const key = ["status", selectedProjectId, selectedProxyId];
    let cancelled = false;
    let retry: ReturnType<typeof setTimeout> | undefined;

    const consume = async () => {
      try {
        for await (const status of statusClient.streamStatus(
          { projectId: selectedProjectId, proxyId: selectedProxyId || undefined },
          { signal: controller.signal },
        )) {
          if (cancelled) return;
          // No guard: React bails out of a state update that sets an equal
          // value, so calling this per message costs nothing after the first.
          // The ref that used to guard it was written during render, which is
          // only safe if every render commits.
          setStreaming(true);
          queryClient.setQueryData(key, status);
        }
      } catch {
        // Server too old, stream blocked, or connection dropped — fall back to
        // polling and try again shortly.
      }

      if (cancelled) return;
      setStreaming(false);
      retry = setTimeout(() => void consume(), RECONNECT_MS);
    };

    void consume();

    return () => {
      cancelled = true;
      controller.abort();
      if (retry) clearTimeout(retry);
      setStreaming(false);
    };
  }, [selectedProjectId, selectedProxyId, queryClient]);

  return query;
}
