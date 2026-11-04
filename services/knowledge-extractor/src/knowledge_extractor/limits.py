"""Service limits: request size, block count/length, time and concurrency."""
from __future__ import annotations

from dataclasses import dataclass

from .schemas import ExtractRequest


@dataclass(frozen=True)
class Limits:
    max_body_bytes: int = 2 * 1024 * 1024
    max_blocks: int = 256
    max_block_chars: int = 20_000
    request_timeout: float = 120.0
    inference_concurrency: int = 1
    max_entities_per_block: int = 512
    max_relations_per_block: int = 1024
    # Chunk window in encoder input tokens (schema prompt + text), see chunking.py.
    window_tokens: int = 512
    window_margin: int = 16
    overlap_tokens: int = 64

    def __post_init__(self) -> None:
        positive = ("max_body_bytes", "max_blocks", "max_block_chars", "inference_concurrency",
                    "max_entities_per_block", "window_tokens")
        for name in positive:
            if getattr(self, name) <= 0:
                raise ValueError(f"{name} must be positive")
        if self.inference_concurrency != 1:
            # GLiNER2's processor mutates shared state per call (mode flag, token caches,
            # collator) and thread safety of concurrent JointIE calls is unproven.
            raise ValueError("inference_concurrency must be 1")
        if self.request_timeout <= 0:
            raise ValueError("request_timeout must be positive")
        if self.max_relations_per_block < 0 or self.window_margin < 0 or self.overlap_tokens < 0:
            raise ValueError("limits must not be negative")
        if self.window_margin >= self.window_tokens:
            raise ValueError("window_margin must be smaller than window_tokens")

    @property
    def token_budget(self) -> int:
        """Largest allowed encoder input, schema prompt included."""
        return self.window_tokens - self.window_margin


class LimitError(Exception):
    def __init__(self, status: int, code: str, message: str) -> None:
        super().__init__(message)
        self.status = status
        self.code = code
        self.message = message


def check_request(request: ExtractRequest, limits: Limits) -> None:
    if len(request.blocks) > limits.max_blocks:
        raise LimitError(413, "too_many_blocks", f"at most {limits.max_blocks} blocks per request")
    for block in request.blocks:
        if len(block.text) > limits.max_block_chars:
            raise LimitError(413, "block_too_long",
                             f"block text exceeds {limits.max_block_chars} code points")


def effective_caps(request: ExtractRequest, limits: Limits) -> tuple[int, int]:
    """Per-block caps: the request may only tighten the service limits."""
    entities = limits.max_entities_per_block
    relations = limits.max_relations_per_block
    if request.limits.max_entities_per_block is not None:
        entities = min(entities, request.limits.max_entities_per_block)
    if request.limits.max_relations_per_block is not None:
        relations = min(relations, request.limits.max_relations_per_block)
    return entities, relations
