export type IntegrationEntry = {
  id: string;
  label: string;
  planned?: boolean;
};

export const integrations: IntegrationEntry[] = [
  {id: "asana", label: "Asana"},
  {id: "attio", label: "Attio"},
  {id: "close", label: "Close"},
  {id: "cloudflare", label: "Cloudflare"},
  {id: "gmail", label: "Gmail"},
  {id: "github", label: "GitHub", planned: true},
  {id: "hubspot", label: "HubSpot"},
  {id: "intercom", label: "Intercom"},
  {id: "mailchimp", label: "Mailchimp"},
  {id: "mailgun", label: "Mailgun"},
  {id: "pipedrive", label: "Pipedrive"},
  {id: "posthog", label: "PostHog"},
  {id: "resend", label: "Resend"},
  {id: "sendgrid", label: "SendGrid"},
  {id: "shopify", label: "Shopify", planned: true},
];

export function composioLogo(id: string, theme?: "dark"): string {
  return `https://logos.composio.dev/api/${id}${theme === "dark" ? "?theme=dark" : ""}`;
}

export function integrationSidebarItems(): {href: string; icon?: string; label: string}[] {
  return [
    {label: "All", href: "/resources/integrations"},
    ...integrations
      .map((entry) => ({
        label: entry.planned ? `${entry.label} (planned)` : entry.label,
        href: `/resources/integrations/${entry.id}`,
        icon: composioLogo(entry.id),
      }))
      .sort((left, right) => left.label.localeCompare(right.label)),
  ];
}
