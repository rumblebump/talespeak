---
id: T-0017
title: Speech to text, private by default
type: feature
priority: medium
created: 2026-10-17
depends: [T-0004, T-0014]
---

## Goal

Players can speak instead of typing. Recognition runs on the device or in the browser, never through a third-party service unless the admin allows it.

## Acceptance

- [ ] Microphone button next to the input line; the recognized text goes into the line for the player to check before sending
- [ ] SpeechRecognition with `processLocally` where the browser offers on-device recognition
- [ ] Fallback: a small Whisper model running in the browser (WASM), served by the server, with its license and size checked
- [ ] Edu and offline servers never use a cloud recognizer; the admin setting for others is documented
- [ ] Works under the strict CSP (`wasm-unsafe-eval` only); Playwright test with a fake audio source

## Notes

Split from T-0004 on 2026-10-17. Section "Speech: listen and speak" in the design doc: https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
