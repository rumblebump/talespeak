---
id: T-0005
title: Whisp tiers 0 and 1 prototype and benchmark
type: research
priority: high
created: 2026-10-17
depends: [T-0002]
---

## Goal

Measure whether fuzzy, phonetic and Model2Vec matching on a 50-entry glossary is fast and accurate enough on a 1 vCPU box.

## Acceptance

- [ ] Pure-Go Model2Vec loader for potion-base-8M
- [ ] Damerau-Levenshtein, Double Metaphone, FTS5 trigram matchers
- [ ] Benchmark: latency per query and top-3 accuracy on 100 talk-around phrases
- [ ] Results in a protocol entry

## Notes

Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
