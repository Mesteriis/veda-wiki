// Wire types of the read-only knowledge routes under /api/v1/knowledge/
// (internal/knowledge, internal/httpapi/knowledge.go). Field names are the
// exact JSON keys; `omitempty` fields are optional, Go nil slices are null.

/** One article text: the snapshot an answer is about. */
export interface SnapshotKey {
  source_id: string;
  path: string;
  content_sha256: string;
}

/** The place in an article text that supports a mention or a claim. */
export interface Evidence extends SnapshotKey {
  pipeline_fingerprint: string;
  block_id: string;
  /** Untrusted article text: always rendered as plain text. */
  quote: string;
  start: number;
  end: number;
  source_start: number;
  source_end: number;
  exact: boolean;
}

export type ResolutionMethod = "technical_key" | "alias" | "same_source_name" | "candidate";

export interface Resolution {
  method: ResolutionMethod;
  basis?: string;
}

export interface Entity {
  id: string;
  type: string;
  name: string;
  key: string;
  aliases?: string[];
  resolution: Resolution;
}

/** An entity with counts over the evidence the caller may read. */
export interface EntitySummary extends Entity {
  mentions: number;
  documents: number;
}

export interface Mention {
  entity_id: string;
  type: string;
  text: string;
  score: number;
  resolution: Resolution;
  evidence: Evidence;
}

export type ClaimStatus = "extracted" | "uncertain";

/** A typed relation extracted from one sentence: head → tail. */
export interface Claim {
  id: string;
  relation: string;
  head_id: string;
  tail_id: string;
  score?: number;
  head_score: number;
  tail_score: number;
  status: ClaimStatus;
  markers?: string[];
  evidence: Evidence;
}

export type ReasonKind = "link" | "shared_entity" | "relation" | "candidate";

export interface Reason {
  kind: ReasonKind;
  entity_id?: string;
  claim_id?: string;
  relation?: string;
  evidence?: Evidence[];
}

export interface RelatedArticle {
  key: SnapshotKey;
  reasons: Reason[];
}

/** GET /api/v1/knowledge/entities */
export interface EntityPage {
  entities: EntitySummary[] | null;
  total: number;
  next?: number;
}

export interface Candidate {
  entity_id: string;
  name: string;
  resolution: Resolution;
}

/** GET /api/v1/knowledge/entity */
export interface EntityCard extends EntitySummary {
  mention_list: Mention[] | null;
  claims: Claim[] | null;
  document_list: SnapshotKey[] | null;
  candidates: Candidate[] | null;
  truncated: boolean;
}

export interface ArticleView {
  key: SnapshotKey;
  entities: EntitySummary[] | null;
  mentions: Mention[] | null;
  claims: Claim[] | null;
  related: RelatedArticle[] | null;
  truncated: boolean;
}

/** Statuses a 200 answer of `article` carries. `disabled` is never sent: clients map 503 knowledge_disabled to it. */
export type ArticleStatus = "processing" | "ready" | "stale" | "unavailable" | "too_large" | "failed";

/** GET /api/v1/knowledge/article */
export interface ArticleKnowledge {
  key: SnapshotKey;
  pipeline_fingerprint?: string;
  status: ArticleStatus;
  /** Only the owner gets the code of a failed extraction. */
  error?: string;
  knowledge?: ArticleView;
}

export type NodeKind = "document" | "entity";

export interface GraphNode {
  id: string;
  kind: NodeKind;
  type?: string;
  label: string;
  key?: SnapshotKey;
}

export type EdgeKind = "relation" | "mention";

export interface GraphEdge {
  id: string;
  kind: EdgeKind;
  from: string;
  to: string;
  relation?: string;
  status?: ClaimStatus;
  markers?: string[];
  count?: number;
  evidence: Evidence[] | null;
}

/** GET /api/v1/knowledge/graph */
export interface Subgraph {
  nodes: GraphNode[] | null;
  edges: GraphEdge[] | null;
  truncated: boolean;
}

/** Server read limits (internal/knowledge/read.go, schema v1). */
export const KNOWLEDGE_LIMITS = { pageLimit: 100, nodesMax: 200, edgesMax: 400, filtersMax: 20, queryRunes: 200 } as const;

/** Relation types of schema v1. */
export const RELATION_TYPES = ["depends_on", "runs_on", "uses", "part_of", "maintained_by", "documents"] as const;
