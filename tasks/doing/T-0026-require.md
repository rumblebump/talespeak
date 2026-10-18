---
id: T-0026
title: Shared Lua modules with require
type: feature
priority: high
created: 2026-10-18
depends: [T-0003]
---

## Goal

A pack writes a helper once and every room script uses it. The content thread asked for it because the demo had to copy a 25-line helper into each room. It is the first core primitive from T-0020: the item take rules will be a Lua module too.

## Acceptance

- [ ] `require("name")` loads `lib/<name>.lua` from the pack. Names are like room ids: no folders and no `.lua`
- [ ] The module runs at most once per script instance, with that instance's globals, and what it returns is cached (true when it returns nothing)
- [ ] A missing module, an error in a module, and a cycle are script errors. A failed require leaves nothing half loaded
- [ ] Modules have the same size and lint rules as scripts, at most 100 per pack, and nothing but modules goes in `lib/`
- [ ] What a module returns counts toward the instance's memory
- [ ] The test pack uses a module, so the Playwright tests run it
- [ ] Docs: `docs/content-format.md` and `docs/lua-sandbox.md`

Design: the Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
