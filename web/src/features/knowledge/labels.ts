// Reader-facing names of the knowledge schema v1 and of how links were found.
import type { ClaimStatus, ReasonKind, ResolutionMethod, SnapshotKey } from "../../api/knowledgeTypes";
import { pageHref } from "../../lib/format";

const entityTypes: Record<string, string> = {
  person: "Человек",
  organization: "Организация",
  project: "Проект",
  service: "Сервис",
  host: "Хост",
  repository: "Репозиторий",
  technology: "Технология",
  document: "Документ",
};

const relations: Record<string, string> = {
  depends_on: "зависит от",
  runs_on: "работает на",
  uses: "использует",
  part_of: "входит в",
  maintained_by: "поддерживается",
  documents: "описывает",
};

const resolutions: Record<ResolutionMethod, string> = {
  technical_key: "по техническому имени",
  alias: "по псевдониму",
  same_source_name: "по имени внутри одного источника",
  candidate: "возможное совпадение",
};

const markers: Record<string, string> = {
  negation: "отрицание",
  plan: "план",
  example: "пример",
  past: "в прошлом",
};

export const reasonLabels: Record<ReasonKind, string> = {
  link: "Ссылка в тексте",
  shared_entity: "Общая сущность",
  relation: "Извлечённое отношение",
  candidate: "Возможное совпадение",
};

export const entityTypeLabel = (type: string | undefined): string => (type && entityTypes[type]) || "Сущность";
export const relationLabel = (relation: string | undefined): string => (relation && relations[relation]) || relation || "связь";
export const resolutionLabel = (method: ResolutionMethod): string => resolutions[method] ?? method;
export const markerLabel = (marker: string): string => markers[marker] ?? marker;

/** Honest label of an extracted relation: never presented as a verified fact. */
export function claimStatusLabel(status: ClaimStatus | undefined, claimMarkers?: string[]): string {
  const base = status === "uncertain" ? "извлечено автоматически, не уверено" : "извлечено автоматически";
  return claimMarkers?.length ? `${base} (${claimMarkers.map(markerLabel).join(", ")})` : base;
}

/** The reader address of an article named by a knowledge snapshot key. */
export const articleHref = (key: Pick<SnapshotKey, "source_id" | "path">): string => pageHref(key.source_id + "/" + key.path);

/** The knowledge graph centred on one entity. */
export const entityGraphHref = (entityID: string): string => "/?graph=1&mode=knowledge&entity=" + encodeURIComponent(entityID);
