import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { isAbort } from "../../api/http";
import { knowledgeEnabled, knowledgeFailure, knowledgeGeneration, subscribeKnowledge, type KnowledgeFailure } from "../../api/knowledge";

export type ReadState<T> =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "done"; data: T }
  | { status: "error"; failure: KnowledgeFailure };

const idle = { status: "idle" } as const;
const loading = { status: "loading" } as const;

interface Stored<T> {
  key: string;
  generation: number;
  state: ReadState<T>;
}

/** The knowledge session generation; it changes on sign-in, sign-out and user change. */
export function useKnowledgeGeneration(): number {
  return useSyncExternalStore(subscribeKnowledge, knowledgeGeneration);
}

/** Whether the module answered as enabled in this session (null: not known yet). */
export function useKnowledgeEnabled(): boolean | null {
  return useSyncExternalStore(subscribeKnowledge, knowledgeEnabled);
}

// Loads one knowledge answer per key. A result is shown only for the key and
// the session it was requested in: a new key or a reset session shows
// loading at once, and the request of the previous one is aborted.
export function useKnowledgeRead<T>(key: string | null, load: (signal: AbortSignal) => Promise<T>): ReadState<T> {
  const generation = useKnowledgeGeneration();
  const [stored, setStored] = useState<Stored<T> | null>(null);
  const loader = useRef(load);
  useEffect(() => { loader.current = load; });
  useEffect(() => {
    if (key === null) return;
    const controller = new AbortController();
    const keep = (state: ReadState<T>) => { if (!controller.signal.aborted) setStored({ key, generation, state }); };
    loader.current(controller.signal).then(
      (data) => keep({ status: "done", data }),
      (error: unknown) => { if (!isAbort(error)) keep({ status: "error", failure: knowledgeFailure(error) }); },
    );
    return () => controller.abort();
  }, [key, generation]);
  if (key === null) return idle;
  if (!stored || stored.key !== key || stored.generation !== generation) return loading;
  return stored.state;
}
