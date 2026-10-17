# Licensing in plain words

The engine is open source and stays open: anyone who changes it must share those changes. Stories and packs belong to their authors, who choose their own license, including commercial ones.

This page is a summary. The binding texts are [LICENSE](../LICENSE) (GNU AGPL v3 or later) and [LICENSE-EXCEPTION.md](../LICENSE-EXCEPTION.md). Both are drafts pending legal review.

## What applies to what

| Part | License | What that means |
| --- | --- | --- |
| Engine: Go code, web client, built-in Lua libraries | AGPL-3.0-or-later | Free to use, study, change and share. Changed versions must stay AGPL, also when only run on a server for others. |
| Your packs: Lua stories, rule packs, glossaries, assets | Your choice | Open, Creative Commons or commercial. The content exception keeps the AGPL from reaching them. |
| Official content shipped with the project | CC-BY-SA 4.0 (planned) | Free to use and remix, with credit, under the same license. |
| DnD 5e SRD material in rule packs | CC-BY-4.0 | Must keep the attribution required by the SRD. |
| Project name and logo | Trademark policy (planned) | Forks are welcome but must use a different name. |

## Common questions

| Question | Answer |
| --- | --- |
| Can a school run the engine with paid story packs? | Yes. Packs that use the public Lua API keep their own license. |
| Can a company sell a story pack? | Yes, under any license it chooses, open or closed. |
| Can I host the unmodified engine for my club or school? | Yes. You only need to point users to this project's source, which the engine does with its built-in Source link. |
| I changed the Go code and host it. What do I owe? | Your users must be able to get your modified source, under the AGPL. Better still, send the change upstream. |
| My pack needs a feature the engine lacks. | Add it to the engine (open, upstream) and use it from your pack through the public API. A pack may not ship patched engine code. |
| Can I fork the project? | Yes. Please rename it, and consider contributing back instead, so everyone gets the improvement. |

## For contributors

Contributions to the engine are accepted under AGPL-3.0-or-later with the content exception. Sign off each commit under the [Developer Certificate of Origin](https://developercertificate.org/) (`git commit -s`); see [CONTRIBUTING.md](../CONTRIBUTING.md). There is no CLA: the base engine stays free forever, so it will never be relicensed or sold under other terms.
