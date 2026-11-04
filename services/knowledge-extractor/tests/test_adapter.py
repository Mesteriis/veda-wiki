from __future__ import annotations

import dataclasses

from fakes import FakeModel
from knowledge_extractor.adapter import extract_block, extract_request
from knowledge_extractor.limits import Limits
from knowledge_extractor.schemas import ExtractRequest
from conftest import request_body

GAZETTEER = {"billing": "service", "db-01": "host", "PostgreSQL": "technology",
             "Иван Петров": "person", "Сервис🚀": "service"}
RELATIONS = [("runs_on", "billing", "db-01"), ("depends_on", "billing", "PostgreSQL")]


def no_deadline() -> None:
    return None


def run(model, schema, text, limits=Limits(), caps=(512, 1024)):
    return extract_block(model, model.prepare(schema), schema, "b1", text, limits, caps, no_deadline)


def assert_quotes(text, result):
    for e in result.entities:
        assert text[e.start:e.end] == e.text


def test_short_block_entities_relations_and_local_ids(schema):
    model = FakeModel(GAZETTEER, RELATIONS)
    text = "Сервис🚀 billing работает на db-01 и зависит от PostgreSQL. Иван Петров."
    result = run(model, schema, text)
    assert_quotes(text, result)
    assert [e.local_id for e in result.entities] == [f"e{i}" for i in range(1, 6)]
    assert [e.text for e in result.entities] == ["Сервис🚀", "billing", "db-01", "PostgreSQL",
                                                 "Иван Петров"]
    first = result.entities[0]
    assert (first.start, first.end) == (0, 7)  # 6 Cyrillic letters + 1 emoji code point
    ids = {e.text: e.local_id for e in result.entities}
    rels = {(r.type, r.head, r.tail) for r in result.relations}
    assert rels == {("runs_on", ids["billing"], ids["db-01"]),
                    ("depends_on", ids["billing"], ids["PostgreSQL"])}
    assert all(r.head_confidence == 0.9 and r.tail_confidence == 0.9 for r in result.relations)
    assert result.chunks[0].status == "ok" and result.chunks[0].feasible


def test_long_block_dedupes_overlap_and_maps_offsets(schema):
    model = FakeModel(GAZETTEER, RELATIONS)
    sentence = "Сервис billing 🚀 на db-01 зависит от PostgreSQL."
    text = " ".join([sentence] * 60) + " Хвост: Иван Петров"
    limits = Limits(window_tokens=70, window_margin=4, overlap_tokens=20)
    result = run(model, schema, text, limits)
    assert len(result.chunks) > 5
    assert any(a.end > b.start for a, b in zip(result.chunks, result.chunks[1:]))
    assert_quotes(text, result)
    keys = [(e.type, e.start, e.end) for e in result.entities]
    assert len(keys) == len(set(keys))
    assert sum(e.text == "billing" for e in result.entities) == 60
    assert result.entities[-1].text == "Иван Петров"  # the tail was processed
    rel_keys = [(r.type, r.head, r.tail) for r in result.relations]
    assert len(rel_keys) == len(set(rel_keys))
    assert result.dropped.invalid_spans == 0


def test_relations_never_cross_chunks(schema):
    model = FakeModel({"billing": "service", "db-01": "host"}, [("runs_on", "billing", "db-01")])
    filler = " ".join(f"слово{i}." for i in range(80))
    text = f"billing. {filler} db-01."
    result = run(model, schema, text, Limits(window_tokens=40, window_margin=2, overlap_tokens=4))
    assert {e.text for e in result.entities} == {"billing", "db-01"}
    assert result.relations == []


def test_infeasible_chunk_is_reported_not_empty_graph(schema):
    model = FakeModel(GAZETTEER, RELATIONS, feasible=lambda t: "PostgreSQL" not in t)
    text = " ".join(["billing on db-01."] * 30 + ["billing needs PostgreSQL."] + ["x."] * 10)
    result = run(model, schema, text, Limits(window_tokens=40, window_margin=2, overlap_tokens=0))
    statuses = [c.status for c in result.chunks]
    assert "infeasible" in statuses
    bad = [c for c in result.chunks if c.status == "infeasible"]
    assert all(not c.feasible for c in bad)
    assert all("PostgreSQL" in text[c.start:c.end] for c in bad)
    assert not any(e.text == "PostgreSQL" for e in result.entities)


def test_single_infeasible_block(schema):
    model = FakeModel(GAZETTEER, RELATIONS, feasible=lambda t: False)
    result = run(model, schema, "billing on db-01.")
    assert [(c.feasible, c.status) for c in result.chunks] == [(False, "infeasible")]
    assert result.entities == [] and result.relations == []


def test_invalid_spans_are_dropped_and_counted(schema):
    def corrupt(entities, text):
        bad = [dataclasses.replace(entities[0], text="wrong"),
               dataclasses.replace(entities[0], start=len(text), end=len(text) + 3),
               dataclasses.replace(entities[0], type="unknown"),
               dataclasses.replace(entities[0], confidence=None),
               dataclasses.replace(entities[0], confidence=float("nan")),
               dataclasses.replace(entities[0], start=5, end=2)]
        return entities[1:] + bad

    model = FakeModel(GAZETTEER, RELATIONS, corrupt=corrupt)
    text = "billing runs on db-01 and needs PostgreSQL."
    result = run(model, schema, text)
    assert result.dropped.invalid_spans == 6
    assert {e.text for e in result.entities} == {"db-01", "PostgreSQL"}
    assert result.relations == []
    assert result.dropped.invalid_relations == 2
    assert_quotes(text, result)


def test_caps_drop_lowest_and_count(schema):
    model = FakeModel(GAZETTEER, RELATIONS)
    text = "billing runs on db-01 and needs PostgreSQL."
    result = run(model, schema, text, caps=(2, 0))
    assert len(result.entities) == 2
    assert result.relations == []
    assert result.dropped.over_limit >= 1


def test_request_echoes_id_model_and_blocks():
    model = FakeModel(GAZETTEER, RELATIONS)
    request = ExtractRequest.model_validate(request_body([("a", "billing on db-01."), ("b", "  ")]))
    response = extract_request(model, request, Limits(), no_deadline)
    dumped = response.model_dump(mode="json")
    assert dumped["schema_version"] == 1 and dumped["request_id"] == "req-1"
    assert dumped["model"] == {"id": "fake", "revision": "0", "device": "cpu"}
    assert [b["id"] for b in dumped["blocks"]] == ["a", "b"]
    assert dumped["blocks"][1] == {"id": "b", "chunks": [], "entities": [], "relations": [],
                                   "dropped": {"invalid_spans": 0, "invalid_relations": 0,
                                               "over_limit": 0}}
