import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { isAbort } from "../api/http";
import { sessionStatus, type SessionPrefix } from "../api/session";

export interface SessionBootstrapOptions {
  /** Shown when the status request fails for any reason except unmounting. */
  unavailableMessage: string;
  onMessage: (message: string) => void;
  /** Loads the authenticated view; a rejection is reported as unavailable too. */
  onAuthenticated: (signal: AbortSignal) => Promise<unknown>;
}

export interface SessionBootstrap {
  /** null until the status request answers. */
  status: boolean | null;
  setStatus: Dispatch<SetStateAction<boolean | null>>;
  /** The session was opened by a reader account, so sign-out belongs to the profile. */
  sharedSession: boolean;
}

/** Asks one session family whether it is signed in, then loads its data. */
export function useSessionBootstrap(prefix: SessionPrefix, options: SessionBootstrapOptions): SessionBootstrap {
  const [status, setStatus] = useState<boolean | null>(null);
  const [sharedSession, setSharedSession] = useState(false);
  // The bootstrap runs once on mount with the callbacks of the first render.
  const initial = useRef(options);
  useEffect(() => {
    const { unavailableMessage, onMessage, onAuthenticated } = initial.current;
    const controller = new AbortController();
    sessionStatus(prefix, controller.signal)
      .then(async (s) => {
        setStatus(s.authenticated);
        setSharedSession(!!s.via_reader_session);
        if (s.authenticated) await onAuthenticated(controller.signal);
      })
      .catch((e: unknown) => { if (!isAbort(e)) onMessage(unavailableMessage); });
    return () => controller.abort();
  }, [prefix]);
  return { status, setStatus, sharedSession };
}
