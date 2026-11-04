import { useEffect, useRef, useState } from "react";
import { HttpError } from "../api/http";
import { fetchState, shouldPoll } from "../api/state";
import type { StatePayload } from "../api/types";

const pollInterval = 10000;

export interface StatePoll {
  data: StatePayload | null;
  error: string;
}

// Polls /api/state for the current address every ten seconds while the tab is
// visible. A change of `restartKey` (the open dialog) restarts polling at once;
// it is a primitive so that a value rebuilt on each render cannot restart it.
export function useStatePoll(restartKey: string | null): StatePoll {
  const [data, setData] = useState<StatePayload | null>(null);
  const [error, setError] = useState("");
  // The tag belongs to the displayed state, so it survives restarts of the
  // polling effect (for example after a dialog closes) together with that state.
  const etag = useRef("");
  useEffect(() => {
    const c = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined, busy = false;
    async function refreshState() {
      clearTimeout(timer);
      timer = undefined;
      if (!shouldPoll(document.visibilityState, etag.current)) return;
      busy = true;
      try {
        const next = await fetchState(fetch, "/api/state" + window.location.search, c.signal, etag.current);
        if (!c.signal.aborted) {
          etag.current = next.etag;
          if (!("unchanged" in next)) setData(next.data);
          setError("");
        }
      } catch (e) {
        if (!c.signal.aborted) {
          const status = e instanceof HttpError ? e.status : 0;
          if (status) etag.current = "";
          if (status === 404) setData(null);
          setError(e instanceof Error ? e.message : "");
        }
      } finally {
        busy = false;
        if (!c.signal.aborted) timer = setTimeout(refreshState, pollInterval);
      }
    }
    // A hidden tab leaves no timer; resume at once when it becomes visible.
    const resume = () => { if (document.visibilityState === "visible" && !busy && timer === undefined) refreshState(); };
    document.addEventListener("visibilitychange", resume);
    refreshState();
    return () => { c.abort(); clearTimeout(timer); document.removeEventListener("visibilitychange", resume); };
  }, [restartKey]);
  return { data, error };
}
