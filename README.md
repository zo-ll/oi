# tide

Small terminal interaction primitives for Go.

Tide is a stdlib-only foundation for terminal apps that need raw input,
alternate-screen rendering, text composition, simple styling, wrapping, and
viewport helpers without adopting a full framework.

It is intentionally small: application state, transcripts, tool events, and
domain-specific rendering belong in the app using Tide.
