from __future__ import annotations

import pytest

from fakes import FakeModel, tokens_for
from knowledge_extractor.chunking import SchemaTooLarge, plan_chunks


def plan(model, schema, text, budget, overlap=0):
    return plan_chunks(text, words=model.words(text), word_tokens=model.word_tokens,
                       input_tokens=lambda t: model.input_tokens(t, schema),
                       budget=budget, overlap=overlap)


def covered(text, chunks):
    marks = [False] * len(text)
    for c in chunks:
        for i in range(c.start, c.end):
            marks[i] = True
    return all(marks[i] for i, ch in enumerate(text) if not ch.isspace())


def test_python_indices_are_code_points():
    # The wire contract counts Unicode code points; Python str indexing does too.
    text = "a😀éж"
    assert len(text) == 5
    assert text[1:2] == "😀" and text[2:4] == "é" and text[4:5] == "ж"
    assert len(text.encode("utf-16-le")) // 2 == 6  # not UTF-16 units
    assert len(text.encode("utf-8")) == 10  # not bytes


def test_short_block_is_one_whole_chunk(schema):
    model = FakeModel()
    chunks = plan(model, schema, "Сервис billing работает.", budget=200)
    assert [(c.start, c.end, c.status) for c in chunks] == [(0, 24, "ok")]


def test_whitespace_block_has_no_chunks(schema):
    assert plan(FakeModel(), schema, "  \n ", budget=200) == []


def test_long_block_respects_exact_budget_and_covers_tail(schema):
    model = FakeModel()
    sentences = [f"Сервис s{i} 🚀 зависит от PostgreSQL и Redis." for i in range(200)]
    text = " ".join(sentences) + " Хвост без точки"
    budget = 60
    chunks = plan(model, schema, text, budget=budget, overlap=12)
    assert len(chunks) > 10
    for c in chunks:
        assert model.input_tokens(text[c.start:c.end], schema) <= budget
    assert covered(text, chunks)
    assert chunks[-1].end == len(text)
    assert text[chunks[-1].start:chunks[-1].end].endswith("Хвост без точки")
    starts = [c.start for c in chunks]
    assert starts == sorted(set(starts))


def test_chunks_prefer_sentence_ends_and_overlap(schema):
    model = FakeModel()
    text = " ".join(f"Sentence number {i} is here." for i in range(40))
    chunks = plan(model, schema, text, budget=50, overlap=10)
    assert all(c.status == "ok" for c in chunks)
    for c in chunks[:-1]:
        assert text[c.start:c.end].endswith(".")
    overlaps = [a.end > b.start for a, b in zip(chunks, chunks[1:])]
    assert any(overlaps)
    for a, b in zip(chunks, chunks[1:]):
        assert b.start > a.start and b.end > a.end


def test_sentence_longer_than_window_is_split_at_words(schema):
    model = FakeModel()
    text = " ".join(f"слово{i}" for i in range(300))  # one sentence, no punctuation
    chunks = plan(model, schema, text, budget=40, overlap=5)
    assert len(chunks) > 1
    assert chunks[0].status == "truncated_prevented"
    assert covered(text, chunks)
    for c in chunks:
        assert c.start == 0 or text[c.start - 1] == " "
        assert c.end == len(text) or text[c.end] == " "


def test_unsplittable_word_is_cut_inside_and_marked(schema):
    model = FakeModel()
    blob = "x" * 900
    text = f"Start. {blob} end."
    budget = 40
    chunks = plan(model, schema, text, budget=budget)
    assert covered(text, chunks)
    assert any(c.status == "truncated_prevented" for c in chunks)
    assert all(model.input_tokens(text[c.start:c.end], schema) <= budget for c in chunks)


def test_combining_marks_and_emoji_keep_offsets(schema):
    model = FakeModel()
    text = ("Café́ 👩‍💻 работает. " * 50).strip()
    chunks = plan(model, schema, text, budget=30, overlap=6)
    assert covered(text, chunks)
    for c in chunks:  # boundaries never split a combining sequence or ZWJ emoji
        assert not ("̀" <= text[c.start] <= "ͯ")
        assert text[c.start] != "‍"


def test_schema_too_large_for_window(schema):
    model = FakeModel()
    text = "word " * 100
    with pytest.raises(SchemaTooLarge):
        plan(model, schema, text, budget=model.overhead(schema) + 3)


def test_token_counts_are_fake_but_consistent():
    assert tokens_for("abcd") == 1 and tokens_for("abcde") == 2
