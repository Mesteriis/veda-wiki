"""Model protocol, raw chunk results and the background loader.

The service talks to the model only through ``ExtractionModel``. The real
implementation (``gliner.GlinerModel``) wraps GLiNER2 JointIE; tests use a
deterministic fake with the same protocol, so no torch import is needed there.
"""
from __future__ import annotations

from dataclasses import dataclass, field
import logging
import threading
import time
from typing import Any, Callable, Protocol, Sequence

from .schemas import ExtractionSchema, ModelInfo

log = logging.getLogger("knowledge_extractor.model")


@dataclass(frozen=True)
class RawEntity:
    id: str
    type: str
    text: str
    start: int
    end: int
    confidence: float | None


@dataclass(frozen=True)
class RawRelation:
    type: str
    head: str
    tail: str
    confidence: float | None


@dataclass(frozen=True)
class RawResult:
    """One model call on one chunk; offsets are relative to the chunk text."""

    feasible: bool
    entities: Sequence[RawEntity] = field(default_factory=tuple)
    relations: Sequence[RawRelation] = field(default_factory=tuple)


class ExtractionModel(Protocol):
    info: ModelInfo

    def prepare(self, schema: ExtractionSchema) -> Any:
        """Compile the request schema once per distinct schema."""

    def words(self, text: str) -> list[tuple[int, int]]:
        """Word spans exactly as the model's word splitter produces them."""

    def word_tokens(self, word: str) -> int:
        """Subword tokens the encoder spends on one word."""

    def input_tokens(self, text: str, prepared: Any) -> int:
        """Exact encoder input length for ``text`` with the schema prompt."""

    def extract(self, text: str, prepared: Any) -> RawResult:
        """Run joint entity/relation extraction on one chunk."""


class ModelUnavailable(Exception):
    """Loading failed for a reason the operator must fix (reported by /ready)."""


SELF_TEST_TEXT = "Alice Smith maintains the billing service, which depends on PostgreSQL."
SELF_TEST_SCHEMA = ExtractionSchema.model_validate({
    "entities": {"person": "a human", "service": "a software service",
                 "technology": "a database or framework"},
    "relations": {"depends_on": {"head": ["service"], "tail": ["technology"], "desc": ""}},
})


def self_test(model: ExtractionModel) -> None:
    """A short real inference must succeed and yield at least one valid span."""
    prepared = model.prepare(SELF_TEST_SCHEMA)
    result = model.extract(SELF_TEST_TEXT, prepared)
    valid = [e for e in result.entities
             if 0 <= e.start < e.end <= len(SELF_TEST_TEXT)
             and SELF_TEST_TEXT[e.start:e.end] == e.text]
    if not valid:
        raise ModelUnavailable("self-test produced no valid entity span")


class ModelHolder:
    """Loads the model once in a background thread and reports readiness."""

    def __init__(self, loader: Callable[[], ExtractionModel]) -> None:
        self._loader = loader
        self._lock = threading.Lock()
        self._state = "loading"
        self._reason = "model is loading"
        self._model: ExtractionModel | None = None
        self._thread: threading.Thread | None = None
        self.load_seconds: float | None = None

    def start(self) -> None:
        with self._lock:
            if self._thread is not None:
                return
            self._thread = threading.Thread(target=self._run, name="model-loader", daemon=True)
            self._thread.start()

    def join(self, timeout: float | None = None) -> None:
        if self._thread is not None:
            self._thread.join(timeout)

    def _run(self) -> None:
        started = time.monotonic()
        try:
            model = self._loader()
            self_test(model)
        except ModelUnavailable as exc:
            self._fail(str(exc))
            return
        except Exception as exc:  # report the class only; messages may hold paths
            log.exception("model load failed")
            self._fail(f"model load failed ({type(exc).__name__}); see service log")
            return
        with self._lock:
            self._model = model
            self._state = "ready"
            self._reason = ""
            self.load_seconds = time.monotonic() - started
        log.info("model ready in %.1fs on %s", self.load_seconds, model.info.device)

    def _fail(self, reason: str) -> None:
        with self._lock:
            self._state = "error"
            self._reason = reason
        log.error("model not ready: %s", reason)

    def status(self) -> tuple[str, str, ExtractionModel | None]:
        with self._lock:
            return self._state, self._reason, self._model
