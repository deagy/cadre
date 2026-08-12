"""Exact language containment for the selector's glob dialect.

Answers one question: is every path matched by glob `I` also matched by at
least one glob in a set `E`? That is language containment, `L(I) ⊆ ⋃L(E)`,
and it is *decidable* here -- the dialect (`**/`, `**`, `*`, `?`, literals)
describes regular languages, so the question has an exact yes/no answer.

It is used to detect a routing rule whose `exclude_paths` fully shadow one of
its own `paths` globs (issue #162), where the answer must be trustworthy in
both directions: a false "shadowed" verdict fails CI on a correct
routing.json, and a false "alive" verdict is the silent coverage loss the
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
`/`, plus one sentinel standing for every other character, plus `\n`. That
abstraction is exact rather than approximate, because the dialect only ever
tests a character for being a specific literal, for being `/`, or for being
anything -- it can never distinguish two characters that appear in no
pattern. `\n` earns its own symbol because the matcher is inconsistent about
it: `**` compiles to `.`, which excludes `\n` without `re.DOTALL`, while
`*`/`?` compile to `[^/]`, which includes it.

The one caveat is case folding: literals are folded with `str.lower()`, which
agrees with `re.IGNORECASE` across ASCII but not across all of Unicode (`ſ`
matches `s` under `IGNORECASE` while `"ſ".lower()` is unchanged; `K` matches
`k` but is not a pattern literal). Every such divergence keeps characters
*apart* that the matcher would merge, which can only cost a missed finding --
never a false one -- and no routing pattern is non-ASCII.

The answer is then computed by searching the product of the include NFA with
the determinized union of the exclude NFAs for a reachable state that accepts
the include and no exclude. Such a state yields a witness path: a concrete
counterexample, returned by `contains_with_witness` so that a NOT_CONTAINED
verdict can be *checked* against `glob_to_regex` rather than trusted. No
witness anywhere in the (finite) product means containment holds.

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
# apart, so a language that contains one contains them all. Abstract symbols
# are opaque tokens rather than characters, so this is deliberately a string
# no single-character pattern literal can ever equal.
_OTHER = "\0other"
_SEPARATOR = "/"
# `\n` needs its own symbol because the matcher treats it inconsistently:
# `glob_to_regex` compiles `**` to `.` (which excludes `\n` without
# re.DOTALL) but `*`/`?` to `[^/]` (which includes it). Folding `\n` into
# _OTHER would model `**` as consuming it, making `contains("foo/*",
# ["foo/**"])` wrongly CONTAINED -- a false accusation, since `foo/a\nb`
# really does match the include and not the exclude.
_NEWLINE = "\n"

# Bound on explored product states. Reaching it yields UNDETERMINED rather
# than a verdict. routing.json's real patterns explore a few dozen.
_MAX_PRODUCT_STATES = 50_000


class _Nfa:
    """An epsilon-NFA whose transitions are predicates over abstract symbols.

    States are allocated as needed rather than one per token, because `**/`
    needs an extra one (see below). State `0` is the start; `self.accept` is
    the single accepting state.
    """

    __slots__ = ("accept", "accepting", "epsilon", "loops", "moves", "_next")

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
        # `glob_to_regex` anchors with `$`, not `\Z`, and `$` also matches
        # immediately before a single trailing newline -- so `glob_to_regex("a")`
        # really does match `"a\n"`. Modelling acceptance as the final state
        # alone would make the include's language smaller than the matcher's,
        # which is again the false-accusation direction.
        trailing_newline = self._allocate()
        self.moves.setdefault(state, []).append((GLOB_LITERAL, _NEWLINE, trailing_newline))
        self.accepting = frozenset({state, trailing_newline})

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
            # `**` compiles to `.`, which excludes `\n`; `*` compiles to
            # `[^/]`, which includes it. The asymmetry is the matcher's, and
            # modelling it is what makes this decision exact.
            if loop == GLOB_DOUBLESTAR and symbol != _NEWLINE:
                nxt.add(state)
            elif loop == GLOB_STAR and symbol != _SEPARATOR:
                nxt.add(state)
            for kind, literal, target in self.moves.get(state, ()):
                if kind == GLOB_LITERAL and symbol == literal:
                    nxt.add(target)
                elif kind == GLOB_QUESTION and symbol != _SEPARATOR:
                    nxt.add(target)
        return self.closure(nxt)


def _alphabet(patterns: Iterable[str]) -> list[str]:
    """The finite abstract alphabet sufficient to decide these patterns."""
    symbols = {_SEPARATOR, _OTHER, _NEWLINE}
    for pattern in patterns:
        for kind, text in iter_glob_tokens(pattern):
            if kind == GLOB_LITERAL:
                symbols.add(text.lower())
    return sorted(symbols)


def _sample_path(pattern: str) -> str | None:
    """A concrete path matching `pattern`, used when there are no excludes."""
    verdict, witness = contains_with_witness(pattern, ["\0impossible-exclude"])
    return witness if verdict == NOT_CONTAINED else None


def _concrete(symbol: str, literals: set[str]) -> str:
    """Render an abstract symbol as a real character, for witness paths."""
    if symbol != _OTHER:
        return symbol
    for candidate in "zqxjkvwy0123456789":
        if candidate not in literals:
            return candidate
    return "￿"


def contains(include: str, excludes: Iterable[str]) -> str:
    """Return CONTAINED, NOT_CONTAINED, or UNDETERMINED for `L(include) ⊆ ⋃L(excludes)`."""
    return contains_with_witness(include, excludes)[0]


def contains_with_witness(include: str, excludes: Iterable[str]) -> tuple[str, str | None]:
    """As `contains`, but also return a concrete counterexample path.

    On NOT_CONTAINED the second element is a path that `include` matches and
    no exclude does -- independently checkable against `glob_to_regex`, which
    is what lets a test verify this verdict rather than take it on trust. It
    is `None` for CONTAINED and UNDETERMINED, where no such path exists or
    none was found.

    Matching is case-insensitive, mirroring `glob_to_regex`'s `re.IGNORECASE`.
    """
    exclude_patterns = list(excludes)
    if not exclude_patterns:
        return NOT_CONTAINED, _sample_path(include)
    include_nfa = _Nfa(include)
    exclude_nfas = [_Nfa(pattern) for pattern in exclude_patterns]
    alphabet = _alphabet([include, *exclude_patterns])

    literals = {
        symbol for symbol in _alphabet([include, *exclude_patterns]) if symbol not in (_OTHER,)
    }

    start = (
        include_nfa.closure([0]),
        tuple(nfa.closure([0]) for nfa in exclude_nfas),
    )
    seen = {start}
    parents: dict[tuple, tuple[tuple, str] | None] = {start: None}
    pending = deque([start])
    while pending:
        if len(seen) > _MAX_PRODUCT_STATES:
            return UNDETERMINED, None
        current = pending.popleft()
        include_states, exclude_states = current
        if (include_nfa.accepting & include_states) and not any(
            nfa.accepting & states for nfa, states in zip(exclude_nfas, exclude_states)
        ):
            # A path matched by the include and by no exclude: proof of life.
            symbols: list[str] = []
            walk = current
            while parents[walk] is not None:
                walk, symbol = parents[walk]  # type: ignore[misc]
                symbols.append(symbol)
            witness = "".join(_concrete(symbol, literals) for symbol in reversed(symbols))
            return NOT_CONTAINED, witness
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
                parents[successor] = (current, symbol)
                pending.append(successor)
    return CONTAINED, None
