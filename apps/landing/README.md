# Fabricate landing page

The landing page is a static Astro site with Tailwind 4. It intentionally
uses native Astro components, semantic HTML, and tiny progressive-enhancement
scripts instead of a client framework. Its CSS tokens use the same Fabricate
visual vocabulary as `apps/docs/theme.css`.

Run it with `pnpm landing:dev`; build with `pnpm landing:build`.

Set `PUBLIC_DOCS_URL` when documentation is on a separate host. The default
target is `/docs`, for a single-domain deployment.

## UI primitives

The page follows shadcn-style conventions (CSS design tokens, composable
components, and accessible native controls) without adding React or a shadcn
runtime. Add shadcn React components only when a genuinely interactive,
stateful control warrants a client island.
