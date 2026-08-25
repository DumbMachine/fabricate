import { defineConfig } from "blume";

export default defineConfig({
  title: "Fabricate",
  logo: {
    image: "/fabricate-mark.svg",
    text: "Fabricate",
  },
  content: {
    root: "../../packages/docs-content",
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
                label: "GitHub (planned)",
                href: "/resources/integrations/github",
                icon: "https://logos.composio.dev/api/github",
              },
            ],
          },
          {
            label: "Environments",
            display: "group",
            collapsed: false,
            items: [
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
              { label: "fab create", href: "/cli/commands/create" },
              { label: "fab ls", href: "/cli/commands/ls" },
              { label: "fab creds", href: "/cli/commands/creds" },
              { label: "fab destroy", href: "/cli/commands/destroy" },
              { label: "fab profiles", href: "/cli/commands/profiles" },
              { label: "fab engines", href: "/cli/commands/engines" },
              { label: "fab wait", href: "/cli/commands/wait" },
              { label: "fab run", href: "/cli/commands/run" },
              { label: "fab logs", href: "/cli/commands/logs" },
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
    output: "static",
  },
});
