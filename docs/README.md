---
doc_title: Documentation Index
summary: Entry point for humans and LLMs. Routes queries to the canonical document with minimal navigation cost.
canonical_for: documentation navigation, doc ownership, anti-duplication rules
keywords:
  - docs
  - documentation
  - navigation
  - semantic search
  - knowledge base
  - protocol
  - replay
---

# Documentation Index

This directory is organized for two goals:

1. low-token navigation for complex implementation work,
2. high retrieval quality for full-text and semantic search.

The key rule is simple: each fact domain has one canonical owner document.

## Fast Path

| If you need | Read first | Read next only if needed |
| --- | --- | --- |
| repository structure, package boundaries, code map | [ARCHITECTURE.md](ARCHITECTURE.md) | [PROTOCOL.md](PROTOCOL.md), [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md) |
| packet format, flags, handshake, wire encoding | [PROTOCOL.md](PROTOCOL.md) | [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md) |
| player input semantics, physics tick order, replay timing | [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md) | [PROTOCOL.md](PROTOCOL.md) |
| ghost replay problem framing, known constraints, failed ideas | [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md) | [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md) |
| current replay implementation strategy and next experiments | [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md) | [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md) |

## Canonical Ownership

- [ARCHITECTURE.md](ARCHITECTURE.md): package responsibilities, dependency direction, code map.
- [PROTOCOL.md](PROTOCOL.md): wire protocol facts, packet formats, message layouts, protocol corrections.
- [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md): input semantics, physics tick order, replay timing details.
- [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md): ghost replay problem statement, constraints, measured failure modes, failed approaches.
- [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md): active implementation plan only.

## LLM-Oriented Conventions

These conventions are based on common RAG and semantic-search guidance for Markdown knowledge bases:

1. Keep one canonical owner per subject area and link instead of restating facts.
2. Put a short summary and searchable keywords at the top of each document.
3. Use stable file names and self-contained section headings so heading-based chunking works well.
4. Prefer heading-sized sections that make sense on their own when retrieved out of context.
5. Store failed experiments in one log instead of scattering them across multiple notes.
6. Route complex work by query decomposition: start with the narrowest canonical document, then expand only if needed.
7. For search and retrieval, combine exact-text search terms with semantic phrases when possible.

## Semantic Search Hints

Use these query styles when searching the repo or a future vector index:

- `ghost replay jump inference momentum recovery`
- `ddnet player input fire parity hook state jump edge`
- `protocol ddnet token appended payload control packet`
- `architecture replay client net6 net7 dependency`

For replay work, the cheapest reliable read order is:

1. [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md)
2. [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md)
3. [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md)
4. [PROTOCOL.md](PROTOCOL.md) only for missing wire-level details

## Anti-Duplication Rule

Before adding new documentation:

1. Check whether the fact already has a canonical owner.
2. If yes, add a link there instead of copying the content.
3. If no, place the new fact in the narrowest canonical document possible.
4. If the fact is a failed replay experiment, append it to [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md) with timestamp and outcome.
