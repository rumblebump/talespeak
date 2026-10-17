# 2026-10-17: GoMud source review

**Result:** GoMud scripting is flexible (Lua and JS), but stats, combat and spells are hardcoded in Go, so it serves as a reference design, not a base.
**Task:** T-0001 (design research)
**Environment:** Claude cloud sandbox, GoMud default branch shallow clone on 2026-10-17.

## Steps

```sh
# get the source
git clone --depth 1 https://github.com/GoMudEngine/GoMud.git
cd GoMud && ls internal

# scripting engines in use
grep -rl "gopher-lua" --include=*.go . | head
sed -n 1,120p internal/scripting/AGENTS.md

# how stats and spells are modelled
sed -n 1,80p internal/stats/stats.go
sed -n 1,80p internal/spells/spells.go

# what scripts can call on an actor
grep -n "^func (a ScriptActor)\|^func (a \*ScriptActor)" internal/scripting/actor_func.go

# combat model and storage
sed -n 1,40p internal/combat/AGENTS.md
grep -n "func " internal/combat/calculations.go | head -15
grep -rli "sqlite" --include=*.go . | head -3
ls _datafiles/world/default/users | head -3
```

```text
go.mod: github.com/dop251/goja, github.com/yuin/gopher-lua …
internal/stats/stats.go: Strength, Speed, Smarts, Vitality, Mysticism, Perception
internal/spells/spells.go: SpellData{Cost, WaitRounds, Difficulty, Type, School}
sqlite: no matches
_datafiles/world/default/users: 1.yaml
```

## Findings

- Two script engines side by side (goja for JS, gopher-lua for Lua), chosen per file extension.
- Scripts can store arbitrary per-character data (`SetMiscCharacterData`), roll dice (`UtilDiceRoll`) and read parties.
- Six fixed non-DnD stats, real-time combat formulas in Go, a fixed spell model, YAML file storage.
- Decision: build a new engine with rules as Lua data packs; keep GoMud as a reference.
