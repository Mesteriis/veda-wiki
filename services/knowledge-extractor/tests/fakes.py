"""Deterministic stand-in for the GLiNER2 backend (same ExtractionModel protocol)."""
from __future__ import annotations

from dataclasses import dataclass, field
import re
import threading
from typing import Callable

from knowledge_extractor.model import RawEntity, RawRelation, RawResult
from knowledge_extractor.schemas import DecoderSettings, ExtractionSchema, ModelIdentity

WORD = re.compile(r"[\ẁ-ͯ‌‍]+(?:[-_][\ẁ-ͯ]+)*|\S")


FAKE_DECODER = DecoderSettings(optimizer="beam", beam_size=32, top_k_entities=32,
                               max_edges_per_type=256, candidate_threshold=0.05,
                               relation_role_threshold=0.05, top_k_roles=12, count_top_k=2,
                               relation_pair_cap=128, entity_threshold=None, library="fake")


def tokens_for(word: str) -> int:
    return max(1, -(-len(word) // 4))


@dataclass
class FakeModel:
    """Finds gazetteer surfaces; relations fire when both surfaces share a chunk."""

    gazetteer: dict[str, str] = field(default_factory=dict)
    relations: list[tuple[str, str, str]] = field(default_factory=list)
    feasible: Callable[[str], bool] = lambda text: True
    corrupt: Callable[[list[RawEntity], str], list[RawEntity]] | None = None
    delay: float = 0.0
    release: threading.Event | None = None
    calls: list[str] = field(default_factory=list)
    info: ModelIdentity = field(default_factory=lambda: ModelIdentity(id="fake", revision="0",
                                                                     device="cpu"))
    decoder: DecoderSettings = field(default_factory=lambda: FAKE_DECODER)

    def prepare(self, schema: ExtractionSchema) -> ExtractionSchema:
        return schema

    def words(self, text: str) -> list[tuple[int, int]]:
        return [(m.start(), m.end()) for m in WORD.finditer(text)]

    def word_tokens(self, word: str) -> int:
        return tokens_for(word)

    def overhead(self, schema: ExtractionSchema) -> int:
        return 4 + 2 * len(schema.entities) + 3 * len(schema.relations)

    def input_tokens(self, text: str, prepared: ExtractionSchema) -> int:
        words = sum(tokens_for(text[s:e]) for s, e in self.words(text))
        dot = 0 if text.endswith((".", "!", "?")) else 1
        return self.overhead(prepared) + words + dot

    def extract(self, text: str, prepared: ExtractionSchema) -> RawResult:
        self.calls.append(text)
        if self.release is not None:
            self.release.wait(5)
        if self.delay:
            threading.Event().wait(self.delay)
        if not self.feasible(text):
            return RawResult(feasible=False)
        entities: list[RawEntity] = []
        by_surface: dict[str, list[str]] = {}
        for surface, kind in self.gazetteer.items():
            if kind not in prepared.entities:
                continue
            for m in re.finditer(re.escape(surface), text):
                local = f"e{len(entities) + 1}"
                entities.append(RawEntity(local, kind, surface, m.start(), m.end(), 0.9))
                by_surface.setdefault(surface, []).append(local)
        if self.corrupt is not None:
            entities = self.corrupt(entities, text)
        rels = [RawRelation(kind, by_surface[h][0], by_surface[t][0], 0.8)
                for kind, h, t in self.relations
                if h in by_surface and t in by_surface and kind in prepared.relations]
        return RawResult(feasible=True, entities=tuple(entities), relations=tuple(rels))
