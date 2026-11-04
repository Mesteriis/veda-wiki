from __future__ import annotations

import dataclasses

import pytest

from fakes import FakeModel
from knowledge_extractor.adapter import extract_block, extract_request
from knowledge_extractor.limits import Limits
from knowledge_extractor.model import RawEntity, RawRelation, RawResult
from knowledge_extractor.schemas import ExtractRequest
from conftest import EXPECTED_MODEL, request_body

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
    assert dumped["model"] == EXPECTED_MODEL
    assert [b["id"] for b in dumped["blocks"]] == ["a", "b"]
    assert dumped["blocks"][1] == {"id": "b", "chunks": [], "entities": [], "relations": [],
                                   "dropped": {"invalid_spans": 0, "partial_spans": 0,
                                               "invalid_relations": 0, "over_limit": 0}}


def test_pipeline_reports_output_relevant_settings():
    model = FakeModel(GAZETTEER, RELATIONS)
    limits = Limits(window_tokens=300, window_margin=10, overlap_tokens=20,
                    max_entities_per_block=50)
    request = ExtractRequest.model_validate(request_body([("a", "billing on db-01.")]))
    pipeline = extract_request(model, request, limits, no_deadline).model.pipeline
    assert (pipeline.window_tokens, pipeline.window_margin, pipeline.overlap_tokens) == (300, 10, 20)
    assert pipeline.max_entities_per_block == 50 and pipeline.chunker_version == 1
    assert pipeline.decoder.optimizer == "beam" and pipeline.decoder.beam_size == 32


def split_at(monkeypatch, text, cuts):
    """Force chunk plans [(start, end), ...] for extract_block."""
    from knowledge_extractor import adapter
    from knowledge_extractor.chunking import ChunkPlan

    plans = [ChunkPlan(start, end, "truncated_prevented") for start, end in cuts]
    monkeypatch.setattr(adapter, "plan_chunks", lambda *a, **k: plans)


def edge_fragments(entities, text):
    """Simulate the model tagging the visible half of a name cut by a chunk edge."""
    extra = []
    if text.endswith("Иван"):
        extra.append(RawEntity("x1", "person", "Иван", len(text) - 4, len(text), 0.95))
    if text.startswith("Петров"):
        extra.append(RawEntity("x2", "person", "Петров", 0, 6, 0.95))
    return entities + extra


def test_chunk_edge_partial_names_are_dropped(schema, monkeypatch):
    text = "Встреча. Ответственный Иван Петров ведёт billing. Потом Петров ушёл."
    ivan = text.index("Иван")
    petrov_end = text.index("Петров") + len("Петров")
    later = text.index("Петров ушёл")
    # chunk 1 ends inside the name, chunk 2 overlaps and holds it whole,
    # chunk 3 starts at the second "Петров" (a real mention, but only at an edge)
    split_at(monkeypatch, text, [(0, ivan + 4), (ivan, petrov_end + 7), (later, len(text))])
    model = FakeModel({"Иван Петров": "person", "billing": "service"}, corrupt=edge_fragments)
    result = run(model, schema, text)
    persons = [(e.text, e.start) for e in result.entities if e.type == "person"]
    assert ("Иван Петров", ivan) in persons
    assert ("Иван", ivan) not in persons
    assert ("Петров", later) in persons  # touches an edge but nothing contains it
    assert result.dropped.partial_spans == 1
    assert_quotes(text, result)


def test_block_edges_never_count_as_chunk_edges(schema, monkeypatch):
    text = "Иван Петров и Иван"
    split_at(monkeypatch, text, [(0, len(text))])
    model = FakeModel({"Иван Петров": "person"}, corrupt=edge_fragments)
    result = run(model, schema, text)
    assert result.dropped.partial_spans == 0
    assert {(e.text, e.start) for e in result.entities} >= {("Иван", 14)}


def test_relations_with_partial_endpoint_are_dropped(schema, monkeypatch):
    text = "Сервис billing работает на db-01-replica."
    cut = text.index("db-01") + len("db-01")
    split_at(monkeypatch, text, [(0, cut), (text.index("billing"), len(text))])

    class Scripted(FakeModel):
        def extract(self, chunk, prepared):
            host = "db-01" if chunk.endswith("db-01") else "db-01-replica"
            b, h = chunk.index("billing"), chunk.index(host)
            return RawResult(True, (RawEntity("e1", "service", "billing", b, b + 7, 0.9),
                                    RawEntity("e2", "host", host, h, h + len(host), 0.8)),
                             (RawRelation("runs_on", "e1", "e2", 0.7),))

    result = run(Scripted(), schema, text)
    assert {e.text for e in result.entities} == {"billing", "db-01-replica"}
    assert result.dropped.partial_spans == 1 and result.dropped.invalid_relations == 1
    [relation] = result.relations
    ids = {e.local_id: e.text for e in result.entities}
    assert (ids[relation.head], ids[relation.tail]) == ("billing", "db-01-replica")


@pytest.mark.parametrize("concurrency", [0, 2, 4])
def test_inference_concurrency_must_be_one(concurrency):
    with pytest.raises(ValueError):
        Limits(inference_concurrency=concurrency)
