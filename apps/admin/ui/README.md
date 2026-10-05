# Commander UI

## Overview

React, TypeScript and Vite frontend for Commander. It follows the platform's CSS
theme, Grunt typography, Lucide icons and typed i18next translations.

This first increment provides the empty connections page, connection-card and
dialog components, and a responsive workspace shell. API calls, authentication,
saved connections and workspace pages follow in later increments. No connection
is simulated; the workspace shell is currently exercised by component tests.

## Quick start

With Node 24+, run from this directory:

```sh
npm ci
npm run dev
```

Open `http://localhost:4275`. `npm run build` writes a standalone build to `dist/`;
`npm run preview` serves that build locally on the same port.

## Development and testing

```sh
npm run typecheck
npm test
npm run build
```

Keep pages in `src/pages`, shared controls in `src/components`, state hooks in
`src/hooks`, and theme/layout CSS in `src/styles`. English strings live in
`src/i18n/en.ts`; use typed translation selectors in components. Behavior tests
live in `tests/`. No runtime imports from sibling repositories are needed.

The COMMANDER wordmark uses the supplied Grunt Grotesk SemiBold, without a star.
Grunt Light and Geist Mono assets and theme colors match the platform frontend.
