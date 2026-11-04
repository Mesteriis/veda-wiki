"""Checks against the pinned GLiNER2.5 checkpoint (``make extractor-real``).

Needs the model in the local Hugging Face cache (scripts/fetch_knowledge_model.py);
runs offline. Model outputs are not hard-coded beyond what the contract guarantees.
"""
from __future__ import annotations

import json
import os
import time

import pytest
from starlette.testclient import TestClient

from conftest import SCHEMA
from knowledge_extractor.adapter import extract_block
from knowledge_extractor.app import create_app
from knowledge_extractor.limits import Limits
from knowledge_extractor.model import ModelHolder
from knowledge_extractor.schemas import ExtractionSchema

pytestmark = pytest.mark.real_model

DEVICE = os.environ.get("EXTRACTOR_TEST_DEVICE", "cpu")
FULL_SCHEMA = ExtractionSchema.model_validate({
    "entities": {"person": "a human", "organization": "a company or team",
                 "project": "a software project", "service": "a running software service",
                 "host": "a server or machine", "repository": "a code repository",
                 "technology": "a language, database or framework", "document": "a document"},
    "relations": {
        "depends_on": {"head": ["service", "project"], "tail": ["service", "technology", "repository"],
                       "desc": "head needs tail to work"},
        "runs_on": {"head": ["service"], "tail": ["host"], "desc": "service is deployed on host"},
        "uses": {"head": ["project", "service", "person"], "tail": ["technology", "service"],
                 "desc": "head uses tail"},
        "part_of": {"head": ["service", "project", "host", "repository"],
                    "tail": ["project", "organization"], "desc": "head is part of tail"},
        "maintained_by": {"head": ["project", "service", "repository"],
                          "tail": ["person", "organization"], "desc": "head is maintained by tail"},
        "documents": {"head": ["document"],
                      "tail": ["project", "service", "host", "repository", "technology"],
                      "desc": "document describes tail"},
    },
})
EN = ("The billing service runs on host db-01 and depends on PostgreSQL. "
      "It is maintained by Alice Smith from Acme Corp.")
RU = ("Сервис billing 🚀 работает на хосте db-01 и зависит от PostgreSQL. "
      "Его поддерживает Иван Петров из компании Ромашка.")


@pytest.fixture(scope="module")
def model():
    from knowledge_extractor.gliner import GlinerModel

    started = time.monotonic()
    loaded = GlinerModel.load(DEVICE, None)
    print(f"\nload {DEVICE}: {time.monotonic() - started:.1f}s")
    return loaded


def check_block(text, result, schema=FULL_SCHEMA):
    ids = {}
    for e in result.entities:
        assert text[e.start:e.end] == e.text
        assert 0.0 <= e.confidence <= 1.0
        ids[e.local_id] = e.type
    for r in result.relations:
        spec = schema.relations[r.type]
        assert ids[r.head] in spec.head and ids[r.tail] in spec.tail
    return {(e.type, e.text) for e in result.entities}


def run(model, text, limits=Limits()):
    started = time.monotonic()
    result = extract_block(model, model.prepare(FULL_SCHEMA), FULL_SCHEMA, "b", text, limits,
                           (limits.max_entities_per_block, limits.max_relations_per_block),
                           lambda: None)
    print(f"\n{len(text)} chars, {len(result.chunks)} chunks: {time.monotonic() - started:.3f}s")
    return result


def test_self_test_and_ready(model):
    holder = ModelHolder(lambda: model)
    holder.start()
    holder.join(60)
    assert holder.status()[0] == "ready"


def test_english_example(model):
    result = run(model, EN)
    found = check_block(EN, result)
    assert ("technology", "PostgreSQL") in found and ("person", "Alice Smith") in found
    assert result.relations, "JointIE returned no relation for an explicit sentence"
    assert all(c.feasible for c in result.chunks)
    print(json.dumps(result.model_dump(mode="json"), ensure_ascii=False))


def test_russian_example_with_emoji(model):
    result = run(model, RU)
    found = check_block(RU, result)
    assert ("person", "Иван Петров") in found
    assert any(t == "technology" and x == "PostgreSQL" for t, x in found)
    print(json.dumps(result.model_dump(mode="json"), ensure_ascii=False))


def test_token_estimate_is_an_upper_bound(model):
    prepared = model.prepare(FULL_SCHEMA)
    for text in (EN, RU, "Café́ 👩‍💻 naïve résumé — ok", "x" * 300):
        estimate = model.input_tokens("", prepared) + sum(
            model.word_tokens(text[s:e]) for s, e in model.words(text))
        assert model.input_tokens(text, prepared) <= estimate


def test_long_mixed_block_is_chunked_within_budget(model):
    paragraph = ("Сервис ledger-{i} работает на хосте node-{i} и зависит от Redis. "
                 "The ledger-{i} service uses Kafka 🚀 for events. ")
    tail = "В конце: сервис tail-api работает на хосте tail-host."
    text = "".join(paragraph.format(i=i) for i in range(60)) + tail
    limits = Limits()
    result = run(model, text, limits)
    prepared = model.prepare(FULL_SCHEMA)
    assert len(result.chunks) > 2
    for c in result.chunks:
        assert model.input_tokens(text[c.start:c.end], prepared) <= limits.token_budget
    assert result.chunks[-1].end == len(text)
    check_block(text, result)
    keys = [(e.type, e.start, e.end) for e in result.entities]
    assert len(keys) == len(set(keys))
    tail_start = len(text) - len(tail)
    assert any(e.start >= tail_start for e in result.entities), "tail was not processed"


def test_http_round_trip(model):
    holder = ModelHolder(lambda: model)
    holder.start()
    holder.join(60)
    key = b"r" * 32
    app = create_app(holder, key, Limits(), allowed_hosts=["testserver"])
    body = {"schema_version": 1, "request_id": "real-1", "schema": SCHEMA,
            "blocks": [{"id": "en", "text": EN}, {"id": "ru", "text": RU}]}
    with TestClient(app) as client:
        assert client.get("/ready").json()["model"]["revision"].startswith("cf5593a5")
        response = client.post("/v1/extract", json=body, headers={"X-Veda-Extractor-Key": key.decode()})
    assert response.status_code == 200
    blocks = response.json()["blocks"]
    assert [b["id"] for b in blocks] == ["en", "ru"]
    for block, text in zip(blocks, (EN, RU)):
        for e in block["entities"]:
            assert text[e["start"]:e["end"]] == e["text"]
