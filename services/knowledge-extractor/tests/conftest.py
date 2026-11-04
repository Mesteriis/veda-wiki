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
