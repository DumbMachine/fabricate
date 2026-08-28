import { defineConfig } from "blume";

const fab = process.env.PUBLIC_FABRICATE_COMMAND || "fab";
const docsBase = process.env.PUBLIC_FABRICATE_DOCS_BASE || "/docs";
const isDevelopment = process.env.PUBLIC_FABRICATE_SITE_MODE === "development";
const replaceFabCommand = (value: string) =>
  value.replace(/(^|[^A-Za-z0-9-])fab(?![A-Za-z0-9-])/g, `$1${fab}`);

export default defineConfig({
  title: "Fabricate",
  integrations: [
    {
      name: "fabricate-mode-aware-commands",
      hooks: {
        "astro:config:setup": ({ updateConfig }) =>
          updateConfig({
            vite: {
              server: {
                // Cursor cloud port-forward hostnames look like
                // <id>-pod-<id>-4322.<region>.cursorvm.com.
                allowedHosts: [".cursorvm.com"],
              },
              plugins: [
                {
                  name: "fabricate-command-substitution",
                  enforce: "pre",
                  transform(code, id) {
                    if (!id.includes("packages/docs-content/") || !/\.mdx?$/.test(id)) return;
                    return { code: replaceFabCommand(code), map: null };
                  },
                },
              ],
            },
          }),
      },
    },
  ],
  logo: {
    image: isDevelopment ? "/fabricate-mark-dev.svg" : "/fabricate-mark.svg",
    text: "Fabricate",
  },
  content: {
    root: process.env.FABRICATE_DOCS_CONTENT_DIR || "../../packages/docs-content",
  },
  theme: {
    accent: "#33c482",
    background: {
      light: "#ffffff",
      dark: "#1b1b1b",
    },
    fonts: {
      display: { name: "Teko", weights: [500, 600, 700] },
      body: "inter",
      mono: "ibm-plex-mono",
    },
    mode: "dark",
  },
  search: {
    provider: "orama",
  },
  navigation: {
    tabs: [
      { label: "Documentation", path: "/", href: "/" },
      { label: "CLI", path: "/cli" },
    ],
    sidebar: [
      {
        label: "Get Started",
        items: [
          { label: "Introduction", href: "/" },
          { label: "Getting Started", href: "/getting-started" },
          { label: "Agent and workflow QA", href: "/guides/agent-workflows" },
        ],
      },
      {
        label: "Resources",
        items: [
          {
            label: "Integrations",
            display: "group",
            collapsed: false,
            items: [
              {
                label: "Gmail",
                href: "/resources/integrations/gmail",
                icon: "https://logos.composio.dev/api/gmail",
              },
              {
                label: "Asana",
                href: "/resources/integrations/asana",
                icon: "https://logos.composio.dev/api/asana",
              },
              {
                label: "HubSpot",
                href: "/resources/integrations/hubspot",
                icon: "https://logos.composio.dev/api/hubspot",
              },
              {
                label: "Intercom",
                href: "/resources/integrations/intercom",
                icon: "https://logos.composio.dev/api/intercom",
              },
              {
                label: "Attio",
                href: "/resources/integrations/attio",
                icon: "https://logos.composio.dev/api/attio",
              },
              {
                label: "Pipedrive",
                href: "/resources/integrations/pipedrive",
                icon: "https://logos.composio.dev/api/pipedrive",
              },
              {
                label: "Close",
                href: "/resources/integrations/close",
                icon: "https://logos.composio.dev/api/close",
              },
              {
                label: "Resend",
                href: "/resources/integrations/resend",
                icon: "https://logos.composio.dev/api/resend",
              },
              {
                label: "Mailgun",
                href: "/resources/integrations/mailgun",
                icon: "https://logos.composio.dev/api/mailgun",
              },
              {
                label: "SendGrid",
                href: "/resources/integrations/sendgrid",
                icon: "https://logos.composio.dev/api/sendgrid",
              },
              {
                label: "Mailchimp",
                href: "/resources/integrations/mailchimp",
                icon: "https://logos.composio.dev/api/mailchimp",
              },
              {
                label: "Exa",
                href: "/resources/integrations/exa",
                icon: "https://logos.composio.dev/api/exa",
              },
              {
                label: "GitHub (planned)",
                href: "/resources/integrations/github",
                icon: "https://logos.composio.dev/api/github",
              },
              {
                label: "Shopify (planned)",
                href: "/resources/integrations/shopify",
                icon: "https://logos.composio.dev/api/shopify",
              },
            ]
              // this is shamefull and needs to be fixed in the future
              .sort((a, b) => a.label.localeCompare(b.label))
          },
          {
            label: "Environments",
            display: "group",
            collapsed: false,
            items: [
              {
                label: "Acme Support Desk",
                href: "/resources/environments/acme-support-desk",
              },
              {
                label: "Acme Billing Ops",
                href: "/resources/environments/acme-billing-ops",
              },
              {
                label: "Acme Go to Market",
                href: "/resources/environments/acme-go-to-market",
              },
              {
                label: "Acme's Gmail",
                href: "/resources/environments/acme-gmail",
              },
            ],
          },
        ],
      },
      {
        // `root` marks this as the /cli tab's sidebar section. Blume renders
        // its children only while that tab is active.
        label: "CLI",
        root: "/cli",
        items: [
          { label: "Overview", href: "/cli" },
          {
            label: "Commands",
            display: "group",
            collapsed: false,
            items: [
              { label: `${fab} run`, href: "/cli/commands/run" },
              { label: `${fab} environment`, href: "/cli/commands/environment" },
              { label: `${fab} service`, href: "/cli/commands/service" },
              { label: `${fab} resource`, href: "/cli/commands/resource" },
              { label: `${fab} scenario`, href: "/cli/commands/scenario" },
              { label: `${fab} logs`, href: "/cli/commands/logs" },
            ],
          },
        ],
      },
    ],
  },
  ai: {
    llmsTxt: true,
  },
  deployment: {
    base: docsBase,
    output: "static",
    site: process.env.PUBLIC_FABRICATE_SITE_URL || "https://fabricate.dmach.in",
  },
});
