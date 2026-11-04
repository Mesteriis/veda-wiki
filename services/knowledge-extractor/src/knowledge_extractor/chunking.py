"""Split one block into model windows by the encoder's real token count.

Coordinates are Python ``str`` indices, i.e. Unicode code points, half-open.

The encoder input for a chunk is ``schema prompt + [SEP_TEXT] + text words``
(GLiNER2 ``SchemaTransformer.transform_record``), and the processor appends a
final ``.`` when the text does not end with sentence punctuation. Therefore
``input_tokens(text) <= input_tokens("") + sum(word_tokens(w))`` and the room
for words is ``budget - input_tokens("")``. Every planned chunk is still
verified with the exact ``input_tokens`` count before it is accepted.

Algorithm (greedy, deterministic):

1. If the whole block fits, it is one chunk ``[0, len(text))``.
2. Otherwise units are the model's words; a word that alone exceeds the room
   is cut into character pieces that fit (status ``truncated_prevented``).
3. A chunk takes as many units as fit, then ends at the last sentence end
   inside it (``.!?…`` or a line break). Without one it ends at a word boundary
   and is marked ``truncated_prevented`` (cut inside a sentence; nothing lost).
4. The next chunk starts up to ``overlap`` tokens back, at the earliest
   sentence start inside that overlap when there is one, always moving forward.
5. The last chunk always ends at the block's last non-space code point, so
   every non-whitespace code point belongs to at least one chunk.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Literal, Sequence

# Bump whenever the chunk plan for the same text and budget can change.
CHUNKER_VERSION = 1
SENTENCE_END = (".", "!", "?", "…", "。", "！", "？")
MIN_ROOM = 8


class SchemaTooLarge(Exception):
    """The schema prompt leaves too little of the window for text."""


@dataclass(frozen=True)
class ChunkPlan:
    start: int
    end: int
    status: Literal["ok", "truncated_prevented"]


@dataclass(frozen=True)
class _Unit:
    start: int
    end: int
    cost: int
    forced: bool


def plan_chunks(text: str, *, words: Sequence[tuple[int, int]],
                word_tokens: Callable[[str], int], input_tokens: Callable[[str], int],
                budget: int, overlap: int) -> list[ChunkPlan]:
    if not words:
        return []
    if input_tokens(text) <= budget:
        return [ChunkPlan(0, len(text), "ok")]
    room = budget - input_tokens("")
    if room < MIN_ROOM:
        raise SchemaTooLarge(f"schema prompt leaves {room} tokens of a {budget}-token window")
    units = _units(text, words, word_tokens, room)
    ends = _sentence_ends(text, units)
    overlap = min(overlap, room // 2)
    chunks: list[ChunkPlan] = []
    i, n, previous_end = 0, len(units), 0
    while i < n:
        k = _verify(text, units, i, _take(units, ends, i, room, previous_end),
                    input_tokens, budget, previous_end)
        if k is None or (previous_end and k <= previous_end):
            # the overlap left no room: restart right after the previous chunk
            if i == previous_end:
                raise SchemaTooLarge("a single text unit does not fit the token window")
            i = previous_end
            continue
        clean = (k == n or ends[k - 1]) and not any(u.forced for u in units[i:k])
        chunks.append(ChunkPlan(units[i].start, units[k - 1].end,
                                "ok" if clean else "truncated_prevented"))
        if k == n:
            break
        previous_end = k
        i = _next_start(units, ends, i, k, overlap, room)
    return chunks


def _units(text: str, words: Sequence[tuple[int, int]],
           word_tokens: Callable[[str], int], room: int) -> list[_Unit]:
    """Model words glued into whitespace-delimited runs where they fit.

    Gluing keeps boundaries off punctuation and ZWJ/emoji sequences; a run
    that does not fit is left as separate words (marked forced).
    """
    units: list[_Unit] = []
    for start, end in words:
        cost = word_tokens(text[start:end])
        if cost > room:
            units.extend(_split_word(text, start, end, word_tokens, room))
            continue
        last = units[-1] if units else None
        if last is not None and last.end == start:
            if not last.forced and last.cost + cost <= room:
                units[-1] = _Unit(last.start, end, last.cost + cost, False)
                continue
            units[-1] = _Unit(last.start, last.end, last.cost, True)
            units.append(_Unit(start, end, cost, True))
            continue
        units.append(_Unit(start, end, cost, False))
    return units


def _split_word(text: str, start: int, end: int,
                word_tokens: Callable[[str], int], room: int) -> list[_Unit]:
    """Cut an over-long word into the longest character pieces that fit."""
    pieces: list[_Unit] = []
    pos = start
    while pos < end:
        lo, hi = pos + 1, end
        while lo < hi:  # largest piece end in [pos+1, end] whose cost fits
            mid = (lo + hi + 1) // 2
            if word_tokens(text[pos:mid]) <= room:
                lo = mid
            else:
                hi = mid - 1
        pieces.append(_Unit(pos, lo, word_tokens(text[pos:lo]), True))
        pos = lo
    return pieces


def _sentence_ends(text: str, units: Sequence[_Unit]) -> list[bool]:
    ends = []
    for index, unit in enumerate(units):
        if unit.forced and index + 1 < len(units) and units[index + 1].start == unit.end:
            ends.append(False)  # inside a split word
            continue
        word = text[unit.start:unit.end]
        gap_end = units[index + 1].start if index + 1 < len(units) else len(text)
        ends.append(word.endswith(SENTENCE_END) or "\n" in text[unit.end:gap_end])
    return ends


def _take(units: Sequence[_Unit], ends: Sequence[bool], i: int, room: int,
          previous_end: int) -> int:
    """End index (exclusive) of the next chunk before exact verification.

    The chunk must reach past the previous chunk's end, so overlap never
    produces a window that is a mere suffix of the previous one.
    """
    acc, j = 0, i
    while j < len(units) and acc + units[j].cost <= room:
        acc += units[j].cost
        j += 1
    if j == len(units):
        return j
    for k in range(j, max(i, previous_end), -1):
        if ends[k - 1]:
            return k
    return j


def _verify(text: str, units: Sequence[_Unit], i: int, k: int,
            input_tokens: Callable[[str], int], budget: int, previous_end: int) -> int | None:
    """Shrink the chunk until the exact encoder input fits the budget.

    Returns None when it would have to end at or before the previous chunk.
    """
    while input_tokens(text[units[i].start:units[k - 1].end]) > budget:
        if k - 1 <= max(i, previous_end):
            return None
        k -= 1
    return k


def _next_start(units: Sequence[_Unit], ends: Sequence[bool], i: int, k: int,
                overlap: int, room: int) -> int:
    """Start of the next chunk: overlap back from ``k`` but leave room for unit ``k``."""
    m, acc = k, 0
    while (m - 1 > i and acc + units[m - 1].cost <= overlap
           and acc + units[m - 1].cost + units[k].cost <= room):
        m -= 1
        acc += units[m].cost
    for p in range(m, k):
        if ends[p - 1]:
            return max(p, i + 1)
    return max(m, i + 1)
