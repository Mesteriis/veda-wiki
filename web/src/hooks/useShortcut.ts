import { useEffect } from "react";

// ⌘K / Ctrl+K style shortcut that focuses an element by its id.
export function useShortcut(key: string, targetId: string): void {
  useEffect(() => {
    const listener = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === key) {
        e.preventDefault();
        document.getElementById(targetId)?.focus();
      }
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [key, targetId]);
}
