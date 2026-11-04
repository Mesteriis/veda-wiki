"""Wire contract v1 between VEDA (Go) and the extractor.

All offsets are half-open ranges of Unicode code points inside a block's text.
Python ``str`` indices are code points, so ``text[start:end]`` is the quote.
"""
from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, StringConstraints, model_validator

SCHEMA_VERSION = 1

Name = Annotated[str, StringConstraints(pattern=r"^[a-z][a-z0-9_]{0,63}$")]
Identifier = Annotated[str, StringConstraints(pattern=r"^[A-Za-z0-9._:/#-]{1,128}$")]
Description = Annotated[str, StringConstraints(max_length=512)]
Status = Literal["ok", "infeasible", "truncated_prevented"]


class Strict(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


class RelationType(Strict):
    head: list[Name] = Field(min_length=1, max_length=32)
    tail: list[Name] = Field(min_length=1, max_length=32)
    desc: Description = ""

    @model_validator(mode="after")
    def _unique(self) -> "RelationType":
        if len(set(self.head)) != len(self.head) or len(set(self.tail)) != len(self.tail):
            raise ValueError("relation endpoint types must be unique")
        return self


class ExtractionSchema(Strict):
    entities: dict[Name, Description] = Field(min_length=1, max_length=32)
    relations: dict[Name, RelationType] = Field(default_factory=dict, max_length=32)

    @model_validator(mode="after")
    def _endpoints_known(self) -> "ExtractionSchema":
        for relation in self.relations.values():
            unknown = (set(relation.head) | set(relation.tail)) - set(self.entities)
            if unknown:
                raise ValueError("relation references an undeclared entity type")
        return self

    def cache_key(self) -> str:
        return self.model_dump_json()


class Block(Strict):
    id: Identifier
    text: str = Field(min_length=1)

    @model_validator(mode="after")
    def _valid_unicode(self) -> "Block":
        try:
            self.text.encode("utf-8")
        except UnicodeEncodeError:
            raise ValueError("block text must be valid Unicode (no lone surrogates)") from None
        return self


class RequestLimits(Strict):
    max_entities_per_block: int | None = Field(default=None, ge=1)
    max_relations_per_block: int | None = Field(default=None, ge=0)


class ExtractRequest(Strict):
    schema_version: Literal[1]
    request_id: Identifier
    schema_: ExtractionSchema = Field(alias="schema")
    blocks: list[Block] = Field(min_length=1)
    limits: RequestLimits = Field(default_factory=RequestLimits)

    model_config = ConfigDict(extra="forbid", frozen=True, populate_by_name=True)

    @model_validator(mode="after")
    def _unique_blocks(self) -> "ExtractRequest":
        ids = [block.id for block in self.blocks]
        if len(set(ids)) != len(ids):
            raise ValueError("block ids must be unique")
        return self


class ModelInfo(Strict):
    id: str
    revision: str
    device: str


class Chunk(Strict):
    start: int
    end: int
    feasible: bool
    status: Status


class Entity(Strict):
    local_id: str
    type: str
    start: int
    end: int
    text: str
    confidence: float


class Relation(Strict):
    type: str
    head: str
    tail: str
    confidence: float | None = None
    head_confidence: float
    tail_confidence: float


class Dropped(Strict):
    """Counters for model output removed by validation (never the raw text)."""

    invalid_spans: int = 0
    invalid_relations: int = 0
    over_limit: int = 0


class BlockResult(Strict):
    id: str
    chunks: list[Chunk]
    entities: list[Entity]
    relations: list[Relation]
    dropped: Dropped


class ExtractResponse(Strict):
    schema_version: Literal[1] = SCHEMA_VERSION
    request_id: str
    model: ModelInfo
    blocks: list[BlockResult]


class ErrorBody(Strict):
    code: str
    message: str


class ErrorResponse(Strict):
    error: ErrorBody
