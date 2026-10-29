# Ruleset test packs

Tiny packs for the tests of rulesets, profiles and continuations (T-0010,
docs/rulesets.md). Point `TALESPEAK_PACKS` at this folder to play them.

- `tiny-rules` 1.0.0 and `tiny-rules-2` 2.0.0: one ruleset in two major versions; 2.0.0 carries `migrations/2.lua`.
- `forest-start`: a story on tiny-rules ^1.0 that exports `flags.rescued_fox`.
- `dragon-isle`: continues forest-start (it must be finished) and imports the flag.
- `sky-tower`: a story on tiny-rules ^2.0, so a 1.x character migrates to enter it.
