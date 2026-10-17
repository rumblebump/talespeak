---
id: T-0009
title: License review and contribution process
type: docs
priority: medium
created: 2026-10-17
depends: [T-0001]
---

## Goal

The AGPL license, the content exception and the licensing FAQ are checked by a lawyer, and the contribution process is chosen before the first outside contribution.

## Acceptance

- [ ] Legal review of LICENSE-EXCEPTION.md and docs/licensing.md
- [x] Decide DCO sign-off or CLA: DCO (Carl, 2026-10-17). The base engine stays free forever, so there is no dual licensing and no CLA
- [x] CONTRIBUTING.md with DCO sign-off (draft, 2026-10-17)
- [ ] DCO check on pull requests (GitHub DCO app or a CI step)
- [ ] Trademark policy for the project name
- [ ] Official content license file under content/ (CC-BY-SA 4.0)

## Notes

Drafts written 2026-10-17. Precedents: GCC Runtime Library Exception, Classpath exception, Luanti (engine LGPL, mods licensed separately).
