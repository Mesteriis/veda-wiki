// Derive a stable VEDA source ID from a GitHub owner/repository reference.
export function githubSourceId(repository: unknown): string {
  const match = String(repository).trim().match(/^(?:https:\/\/github\.com\/)?([A-Za-z0-9][A-Za-z0-9_.-]*)\/([A-Za-z0-9][A-Za-z0-9_.-]*?)(?:\.git)?\/?$/i);
  if (!match) return "";
  const part = (s: string) => s.toLowerCase().replace(/[^a-z0-9_-]/g, "-");
  return `github-${part(match[1] ?? "")}-${part(match[2] ?? "")}`.slice(0, 64).replace(/-+$/, "");
}
