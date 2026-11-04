"""Real GLiNER2.5 backend (JointIE over the pinned checkpoint).

Imported only by the running service and the real-model tests: it pulls in
torch and transformers. The checkpoint is always resolved from the local
Hugging Face cache (``local_files_only=True``); the service never downloads.
"""
from __future__ import annotations

import os

# The service never downloads: force the Hugging Face libraries offline before
# gliner2/transformers/huggingface_hub are imported (they read these at import).
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")

from typing import Any  # noqa: E402

from .model import ModelUnavailable, RawEntity, RawRelation, RawResult, SchemaCache  # noqa: E402
from .schemas import DecoderSettings, ExtractionSchema, ModelIdentity  # noqa: E402

MODEL_ID = "fastino/gliner2.5-multi-v1"
MODEL_REVISION = "cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3"
FETCH_HINT = ("fetch it with: uv run --project services/knowledge-extractor "
              "python scripts/fetch_knowledge_model.py")
REQUIRED_FILES = ("config.json", "encoder_config/config.json", "model.safetensors",
                  "tokenizer.json", "tokenizer_config.json")
DEVICES = ("cpu", "mps", "cuda")
# JointIEConfig values pinned explicitly (they equal gliner2 2.1.0 defaults) so that
# they can be reported in the pipeline description; changing any of them changes output.
DECODER = dict(optimizer="beam", beam_size=32, top_k_entities=32, max_edges_per_type=256,
               candidate_threshold=0.05, relation_role_threshold=0.05, top_k_roles=12,
               count_top_k=2, relation_pair_cap=128, entity_threshold=None)


def check_device(device: str) -> None:
    """Fail loudly when the requested device is unusable; never fall back."""
    import torch

    if device not in DEVICES:
        raise ModelUnavailable(f"unknown device {device!r}; use one of {', '.join(DEVICES)}")
    if device == "mps":
        if not torch.backends.mps.is_built():
            raise ModelUnavailable("device mps unavailable: this torch build has no MPS support")
        if not torch.backends.mps.is_available():
            raise ModelUnavailable("device mps unavailable: torch.backends.mps.is_available() is False")
    if device == "cuda" and not torch.cuda.is_available():
        raise ModelUnavailable("device cuda unavailable: torch.cuda.is_available() is False")


def resolve_snapshot(cache_dir: str | None) -> str:
    """Local directory of the pinned revision, or a readable cache error."""
    from huggingface_hub import snapshot_download
    from huggingface_hub.errors import LocalEntryNotFoundError

    try:
        path = snapshot_download(repo_id=MODEL_ID, revision=MODEL_REVISION,
                                 cache_dir=cache_dir, local_files_only=True)
    except LocalEntryNotFoundError:
        raise ModelUnavailable(
            f"model {MODEL_ID}@{MODEL_REVISION} is not in the local Hugging Face cache; "
            f"{FETCH_HINT}") from None
    missing = [name for name in REQUIRED_FILES if not os.path.isfile(os.path.join(path, name))]
    if missing:
        raise ModelUnavailable(
            f"cached model {MODEL_ID}@{MODEL_REVISION} is incomplete "
            f"({len(missing)} required files missing); {FETCH_HINT}")
    return path


class GlinerModel:
    """``ExtractionModel`` over GLiNER2 ``JointIE`` with the boundary checkpoint."""

    def __init__(self, extractor: Any, device: str) -> None:
        import gliner2
        from gliner2.joint_ie import JointIE, JointIEConfig

        self._extractor = extractor
        self._processor = extractor.processor
        self._engine = JointIE(extractor)
        self._config = JointIEConfig(include_confidence=True, include_spans=True, **DECODER)
        # Own bounded cache; compile_schema() is called directly so the engine's
        # unbounded _compiled_schemas dict is never populated.
        self._schemas = SchemaCache(self._compile, size=16)
        self.info = ModelIdentity(id=MODEL_ID, revision=MODEL_REVISION, device=device)
        self.decoder = DecoderSettings(library=f"gliner2=={gliner2.__version__}", **DECODER)

    @classmethod
    def load(cls, device: str, cache_dir: str | None) -> "GlinerModel":
        check_device(device)
        path = resolve_snapshot(cache_dir)
        from gliner2 import AutoExtractor

        # A local snapshot path pins config, weights *and* tokenizer to the same
        # revision; map_location is a documented loader option.
        extractor = AutoExtractor.from_pretrained(path, map_location=device)
        extractor.eval()
        return cls(extractor, device)

    @staticmethod
    def _compile(schema: ExtractionSchema) -> Any:
        from gliner2.joint_ie import JointSchema, compile_schema

        joint = JointSchema()
        for name, desc in schema.entities.items():
            joint.entity(name, desc or None)
        for name, rel in schema.relations.items():
            joint.relation(name, rel.head, rel.tail, rel.desc or None)
        return compile_schema(joint)

    def prepare(self, schema: ExtractionSchema) -> Any:
        return self._schemas.get(schema)

    def words(self, text: str) -> list[tuple[int, int]]:
        return [(start, end) for _, start, end in self._processor.word_splitter(text, lower=True)]

    def word_tokens(self, word: str) -> int:
        return len(self._processor.tokenizer.tokenize(word.lower()))

    def input_tokens(self, text: str, prepared: Any) -> int:
        return len(self._processor.transform_record(text, prepared).input_ids)

    def extract(self, text: str, prepared: Any) -> RawResult:
        result = self._engine.extract(text, prepared, config=self._config)
        entities = tuple(
            RawEntity(id=e.id, type=e.type, text=e.text, start=e.start, end=e.end,
                      confidence=float(e.confidence) if e.confidence is not None else None)
            for e in result.entities)
        relations = tuple(
            RawRelation(type=r.type, head=r.head, tail=r.tail,
                        confidence=float(r.confidence) if r.confidence is not None else None)
            for r in result.relations)
        return RawResult(feasible=bool(result.feasible), entities=entities, relations=relations)
