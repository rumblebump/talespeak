# Talespeak Content Exception, version 1.0

**Draft. Have this reviewed by a lawyer before the first public release.**

This is an additional permission under section 7 of the GNU Affero General Public License, version 3 (the "AGPL"). It applies to the Talespeak engine, which is licensed under the AGPL version 3 or (at your option) any later version. The full AGPL text is in [LICENSE](LICENSE).

## 1. Definitions

- **"Engine"** means the software in this repository that is licensed under the AGPL: the Go source code, the web client, the Lua libraries shipped with the engine, and any modified version of them. Files that state a different license (for example official content under `content/`) are not part of the Engine.
- **"Public API"** means the interfaces the Engine documents for use by Content: the Lua functions, events and objects listed in the engine's API documentation, the file formats for stories, rule packs, glossaries and asset manifests, and the client message protocol.
- **"Content"** means stories, rule packs, Lua scripts, data files (such as YAML or JSON), glossaries, images, audio, music, fonts, video and other assets that the Engine loads or runs, and that interact with the Engine only through the Public API.

## 2. Permission

As a special exception, the copyright holders of the Engine give you permission to write, use, run, copy, distribute, sell and license Content under terms of your choice, open or proprietary, and to load and run such Content with the Engine, including on a server that others use over a network.

Content is not a work based on the Engine merely because it is loaded, interpreted or executed by the Engine, or because it uses the Public API. Neither you nor the Content become subject to the AGPL's terms by doing so.

## 3. What this exception does not cover

1. **The Engine itself.** The AGPL, including section 13 (Remote Network Interaction), still applies in full to the Engine and to any modified version of it. If you change the Engine and let people use it over a network, you must offer them its source code.
2. **Content that contains the Engine.** Content that includes, copies, bundles or patches any part of the Engine, modified or not, is not covered by this exception for that part.
3. **Content that bypasses the Public API.** Content that loads native code into the Engine, or relies on undocumented internal interfaces of the Engine, is not covered by this exception.
4. **Trademarks.** This exception grants no right to use the project's name or logos.

## 4. Modified versions of the Engine

If you modify the Engine, you may extend this exception to your version, but you are not obliged to. If you do not wish to do so, delete this exception statement from your version.

## 5. Contributions

Contributions to the Engine are accepted under the AGPL version 3 or later together with this exception, so that the permission in section 2 covers the whole Engine.
