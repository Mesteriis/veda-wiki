from __future__ import annotations

import copy

import pytest

from knowledge_extractor.schemas import ExtractionSchema

SCHEMA = {
    "entities": {"person": "a human", "service": "a software service", "host": "a machine",
                 "technology": "a language, database or framework"},
    "relations": {
        "runs_on": {"head": ["service"], "tail": ["host"], "desc": "service is deployed on host"},
        "depends_on": {"head": ["service"], "tail": ["service", "technology"], "desc": ""},
    },
}


@pytest.fixture
def schema() -> ExtractionSchema:
    return ExtractionSchema.model_validate(SCHEMA)


def request_body(blocks, **extra):
    body = {"schema_version": 1, "request_id": "req-1", "schema": copy.deepcopy(SCHEMA),
            "blocks": [{"id": bid, "text": text} for bid, text in blocks]}
    body.update(extra)
    return body


EXPECTED_DECODER = {"optimizer": "beam", "beam_size": 32, "top_k_entities": 32,
                    "max_edges_per_type": 256, "candidate_threshold": 0.05,
                    "relation_role_threshold": 0.05, "top_k_roles": 12, "count_top_k": 2,
                    "relation_pair_cap": 128, "entity_threshold": None, "library": "fake"}
EXPECTED_MODEL = {"id": "fake", "revision": "0", "device": "cpu",
                  "pipeline": {"model_id": "fake", "model_revision": "0", "chunker_version": 1,
                               "window_tokens": 512, "window_margin": 16, "overlap_tokens": 64,
                               "max_entities_per_block": 512, "max_relations_per_block": 1024,
                               "decoder": EXPECTED_DECODER}}
