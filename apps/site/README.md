# Fabricate public site

`pnpm site:build` produces one static release bundle:

- `/` is the Astro landing page;
- `/docs/` is the Blume documentation site.

`pnpm site:deploy` deploys that bundle to the `fabricate.dmach.in` Cloudflare
Worker custom domain. The Worker uses Cloudflare static-asset handling, so
`/docs` permanently normalizes to `/docs/` and both surfaces remain static.
