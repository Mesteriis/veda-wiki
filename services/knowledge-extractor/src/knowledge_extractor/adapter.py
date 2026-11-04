"""Turn per-chunk model output into validated block results.

Chunk offsets are shifted to block code points, every span is checked against
the block text, overlap duplicates are merged by ``(type, start, end)`` and
relations are rewritten to block-local entity ids. Relations are only taken
from a single chunk, so both endpoints always come from the same context.
"""
from __future__ import annotations

from dataclasses import dataclass, field
import math
from typing import Any, Callable

from .chunking import plan_chunks
from .limits import Limits, effective_caps
from .model import ExtractionModel, RawEntity, RawResult
from .schemas import (BlockResult, Chunk, Dropped, Entity, ExtractionSchema, ExtractRequest,
                      ExtractResponse, Relation)

SpanKey = tuple[str, int, int]


class DeadlineExceeded(Exception):
    """The request ran past its time limit; remaining chunks were skipped."""


@dataclass
class _Found:
    text: str
    confidence: float


@dataclass
class _Edge:
    confidence: float | None
    head_confidence: float
    tail_confidence: float


@dataclass
class _Block:
    entities: dict[SpanKey, _Found] = field(default_factory=dict)
    edges: dict[tuple[str, SpanKey, SpanKey], _Edge] = field(default_factory=dict)
    invalid_spans: int = 0
    invalid_relations: int = 0


def _score(value: float | None) -> bool:
    return value is not None and math.isfinite(value) and 0.0 <= value <= 1.0


def _valid_entity(e: RawEntity, chunk: str, schema: ExtractionSchema) -> bool:
    return (e.type in schema.entities and _score(e.confidence)
            and isinstance(e.start, int) and isinstance(e.end, int)
            and 0 <= e.start < e.end <= len(chunk) and chunk[e.start:e.end] == e.text)


def _merge_chunk(acc: _Block, raw: RawResult, block_text: str, offset: int, end: int,
                 schema: ExtractionSchema) -> None:
    chunk = block_text[offset:end]
    local: dict[str, tuple[SpanKey, float]] = {}
    for e in raw.entities:
        if not _valid_entity(e, chunk, schema):
            acc.invalid_spans += 1
            continue
        key = (e.type, offset + e.start, offset + e.end)
        if block_text[key[1]:key[2]] != e.text:
            acc.invalid_spans += 1
            continue
        confidence = float(e.confidence)
        local[e.id] = (key, confidence)
        found = acc.entities.get(key)
        if found is None or confidence > found.confidence:
            acc.entities[key] = _Found(e.text, confidence)
    for r in raw.relations:
        spec = schema.relations.get(r.type)
        head, tail = local.get(r.head), local.get(r.tail)
        if (spec is None or head is None or tail is None or head[0] == tail[0]
                or head[0][0] not in spec.head or tail[0][0] not in spec.tail
                or (r.confidence is not None and not _score(r.confidence))):
            acc.invalid_relations += 1
            continue
        key = (r.type, head[0], tail[0])
        edge = _Edge(r.confidence, head[1], tail[1])
        best = acc.edges.get(key)
        if best is None or (edge.confidence or 0.0) > (best.confidence or 0.0):
            acc.edges[key] = edge


def _finish(block_id: str, chunks: list[Chunk], acc: _Block,
            max_entities: int, max_relations: int) -> BlockResult:
    over = 0
    ranked = sorted(acc.entities.items(), key=lambda kv: (-kv[1].confidence, kv[0][1], kv[0][2], kv[0][0]))
    kept = dict(ranked[:max_entities])
    over += len(ranked) - len(kept)
    ordered = sorted(kept, key=lambda k: (k[1], k[2], k[0]))
    ids = {key: f"e{index}" for index, key in enumerate(ordered, start=1)}
    entities = [Entity(local_id=ids[k], type=k[0], start=k[1], end=k[2], text=kept[k].text,
                       confidence=kept[k].confidence) for k in ordered]
    edges = [(key, edge) for key, edge in acc.edges.items() if key[1] in ids and key[2] in ids]
    over += len(acc.edges) - len(edges)
    edges.sort(key=lambda kv: (-(kv[1].confidence or 0.0), kv[0][0], kv[0][1], kv[0][2]))
    over += max(0, len(edges) - max_relations)
    edges = edges[:max_relations]
    position = {key: index for index, key in enumerate(ordered)}
    edges.sort(key=lambda kv: (position[kv[0][1]], position[kv[0][2]], kv[0][0]))
    relations = [Relation(type=key[0], head=ids[key[1]], tail=ids[key[2]], confidence=edge.confidence,
                          head_confidence=edge.head_confidence, tail_confidence=edge.tail_confidence)
                 for key, edge in edges]
    dropped = Dropped(invalid_spans=acc.invalid_spans, invalid_relations=acc.invalid_relations,
                      over_limit=over)
    return BlockResult(id=block_id, chunks=chunks, entities=entities, relations=relations,
                       dropped=dropped)


def extract_block(model: ExtractionModel, prepared: Any, schema: ExtractionSchema, block_id: str,
                  text: str, limits: Limits, caps: tuple[int, int],
                  check_deadline: Callable[[], None]) -> BlockResult:
    plans = plan_chunks(text, words=model.words(text), word_tokens=model.word_tokens,
                        input_tokens=lambda t: model.input_tokens(t, prepared),
                        budget=limits.token_budget, overlap=limits.overlap_tokens)
    acc = _Block()
    chunks: list[Chunk] = []
    for plan in plans:
        check_deadline()
        raw = model.extract(text[plan.start:plan.end], prepared)
        if not raw.feasible:
            # JointIE could not satisfy the hard constraints: report it, keep no output.
            chunks.append(Chunk(start=plan.start, end=plan.end, feasible=False, status="infeasible"))
            continue
        chunks.append(Chunk(start=plan.start, end=plan.end, feasible=True, status=plan.status))
        _merge_chunk(acc, raw, text, plan.start, plan.end, schema)
    return _finish(block_id, chunks, acc, *caps)


def extract_request(model: ExtractionModel, request: ExtractRequest, limits: Limits,
                    check_deadline: Callable[[], None]) -> ExtractResponse:
    schema = request.schema_
    prepared = model.prepare(schema)
    caps = effective_caps(request, limits)
    blocks = [extract_block(model, prepared, schema, block.id, block.text, limits, caps,
                            check_deadline)
              for block in request.blocks]
    return ExtractResponse(request_id=request.request_id, model=model.info, blocks=blocks)
