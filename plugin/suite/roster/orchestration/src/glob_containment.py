"""Exact language containment for the selector's glob dialect.

Answers one question: is every path matched by glob `I` also matched by at
least one glob in a set `E`? That is language containment, `L(I) ⊆ ⋃L(E)`,
and it is *decidable* here -- the dialect (`**/`, `**`, `*`, `?`, literals)
describes regular languages, so the question has an exact yes/no answer.

It is used to detect a routing rule whose `exclude_paths` fully shadow one of
its own `paths` globs (issue #162), where the answer must be trustworthy in
both directions: a false "shadowed" verdict fails CI on a correct
routing.yaml, and a false "alive" verdict is the silent coverage loss the
check exists to catch.

An earlier version of that check sampled synthesized probe paths and reported
a glob when every probe was excluded. Sampling cannot support that verdict:
the finding is a *universal* claim, so an incomplete sample makes it strictly
easier to satisfy, which is the false-accusation direction. Concretely,
`paths: ["roster/**"]` with `exclude_paths: ["**/*.txt"]` was reported as
fully shadowed because every synthesized probe happened to end in `.txt`.
This module replaces that guess with a decision procedure.

**Method.** Each glob becomes an NFA over a finite *abstract* alphabet: the
distinct literal characters appearing in the globs under comparison, plus
`/`, plus one sentinel standing for every other character. That abstraction
is exact rather than approximate, because the dialect only ever tests a
character for being a specific literal, for being `/`, or for being anything
-- it can never distinguish two characters that appear in no pattern. The
answer is then computed by searching the product of the include NFA with the
determinized union of the exclude NFAs for a reachable state that accepts the
include and no exclude. Such a state is a witness path: proof the glob is
alive. No witness anywhere in the (finite) product means containment holds.

The search is bounded by `_MAX_PRODUCT_STATES`. Exhausting it returns
`UNDETERMINED` -- never a verdict -- so a caller that reports only on
`CONTAINED` cannot be made to accuse falsely by a pathological pattern.
"""

from __future__ import annotations

from collections import deque
from typing import Iterable

from routing import (
    GLOB_DOUBLESTAR,
    GLOB_DOUBLESTAR_SLASH,
    GLOB_LITERAL,
    GLOB_QUESTION,
    GLOB_STAR,
    iter_glob_tokens,
)

CONTAINED = "contained"
NOT_CONTAINED = "not-contained"
UNDETERMINED = "undetermined"

# The sentinel standing for "any character that appears in no pattern under
# comparison". One is sufficient: the dialect cannot tell two such characters
# apart, so a language that contains one contains them all.
_OTHER = "\0"
_SEPARATOR = "/"

# Bound on explored product states. Reaching it yields UNDETERMINED rather
# than a verdict. routing.yaml's real patterns explore a few dozen.
_MAX_PRODUCT_STATES = 50_000


class _Nfa:
    """An epsilon-NFA whose transitions are predicates over abstract symbols.

    States are allocated as needed rather than one per token, because `**/`
    needs an extra one (see below). State `0` is the start; `self.accept` is
    the single accepting state.
    """

    __slots__ = ("accept", "epsilon", "loops", "moves", "_next")

    def __init__(self, pattern: str) -> None:
        # moves[state] -> list of (predicate_kind, literal_or_None, target)
        self.moves: dict[int, list[tuple[str, str | None, int]]] = {}
        self.loops: dict[int, str] = {}
        self.epsilon: dict[int, list[int]] = {}
        self._next = 1
        state = 0
        for kind, text in iter_glob_tokens(pattern):
            target = self._allocate()
            if kind == GLOB_LITERAL:
                self.moves.setdefault(state, []).append((GLOB_LITERAL, text.lower(), target))
            elif kind == GLOB_QUESTION:
                self.moves.setdefault(state, []).append((GLOB_QUESTION, None, target))
            elif kind == GLOB_STAR:
                # `[^/]*`: consume non-separators in place, then move on. The
                # skip stays available after looping, which is correct -- the
                # loop and the exit are the same alternative.
                self.loops[state] = GLOB_STAR
                self.epsilon.setdefault(state, []).append(target)
            elif kind == GLOB_DOUBLESTAR:
                # `.*`: same shape, over every symbol.
                self.loops[state] = GLOB_DOUBLESTAR
                self.epsilon.setdefault(state, []).append(target)
            elif kind == GLOB_DOUBLESTAR_SLASH:
                # `(?:.*/)?` is a *choice* between nothing and "anything, then
                # `/`" -- and the second branch, once entered, is committed to
                # ending on a separator. Modelling it as a self-loop plus an
                # epsilon skip on one state conflates the two: after the loop
                # consumed a character the skip would still be reachable, so
                # `**/a` would wrongly accept `.a`. The committed branch gets
                # its own state.
                consumed = self._allocate()
                self.epsilon.setdefault(state, []).extend((consumed, target))
                self.loops[consumed] = GLOB_DOUBLESTAR
                self.moves.setdefault(consumed, []).append((GLOB_LITERAL, _SEPARATOR, target))
            state = target
        self.accept = state

    def _allocate(self) -> int:
        state = self._next
        self._next += 1
        return state

    def closure(self, states: Iterable[int]) -> frozenset[int]:
        pending = deque(states)
        seen = set(pending)
        while pending:
            state = pending.popleft()
            for target in self.epsilon.get(state, ()):
                if target not in seen:
                    seen.add(target)
                    pending.append(target)
        return frozenset(seen)

    def step(self, states: frozenset[int], symbol: str) -> frozenset[int]:
        nxt: set[int] = set()
        for state in states:
            loop = self.loops.get(state)
            if loop == GLOB_DOUBLESTAR or (loop == GLOB_STAR and symbol != _SEPARATOR):
                nxt.add(state)
            for kind, literal, target in self.moves.get(state, ()):
                if kind == GLOB_LITERAL and symbol == literal:
                    nxt.add(target)
                elif kind == GLOB_QUESTION and symbol != _SEPARATOR:
                    nxt.add(target)
        return self.closure(nxt)


def _alphabet(patterns: Iterable[str]) -> list[str]:
    """The finite abstract alphabet sufficient to decide these patterns."""
    symbols = {_SEPARATOR, _OTHER}
    for pattern in patterns:
        for kind, text in iter_glob_tokens(pattern):
            if kind == GLOB_LITERAL:
                symbols.add(text.lower())
    return sorted(symbols)


def contains(include: str, excludes: Iterable[str]) -> str:
    """Return CONTAINED, NOT_CONTAINED, or UNDETERMINED for `L(include) ⊆ ⋃L(excludes)`.

    Matching is case-insensitive, mirroring `glob_to_regex`'s `re.IGNORECASE`.
    """
    exclude_patterns = list(excludes)
    if not exclude_patterns:
        return NOT_CONTAINED
    include_nfa = _Nfa(include)
    exclude_nfas = [_Nfa(pattern) for pattern in exclude_patterns]
    alphabet = _alphabet([include, *exclude_patterns])

    start = (
        include_nfa.closure([0]),
        tuple(nfa.closure([0]) for nfa in exclude_nfas),
    )
    seen = {start}
    pending = deque([start])
    while pending:
        if len(seen) > _MAX_PRODUCT_STATES:
            return UNDETERMINED
        include_states, exclude_states = pending.popleft()
        if include_nfa.accept in include_states and not any(
            nfa.accept in states for nfa, states in zip(exclude_nfas, exclude_states)
        ):
            # A path matched by the include and by no exclude: proof of life.
            return NOT_CONTAINED
        for symbol in alphabet:
            successor = (
                include_nfa.step(include_states, symbol),
                tuple(nfa.step(states, symbol) for nfa, states in zip(exclude_nfas, exclude_states)),
            )
            if not successor[0]:
                # The include can no longer match; this branch proves nothing.
                continue
            if successor not in seen:
                seen.add(successor)
                pending.append(successor)
    return CONTAINED
