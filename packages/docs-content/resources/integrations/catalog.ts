export type IntegrationEntry = {
  id: string;
  label: string;
  planned?: boolean;
};

export const integrations: IntegrationEntry[] = [
  {id: "airbyte", label: "Airbyte"},
  {id: "asana", label: "Asana"},
  {id: "attio", label: "Attio"},
  {id: "bamboohr", label: "BambooHR"},
  {id: "canny", label: "Canny"},
  {id: "carta", label: "Carta"},
  {id: "chargebee", label: "Chargebee"},
  {id: "close", label: "Close"},
  {id: "cloudflare", label: "Cloudflare"},
  {id: "digitalocean", label: "DigitalOcean"},
  {id: "docusign", label: "DocuSign"},
  {id: "gmail", label: "Gmail"},
  {id: "greenhouse", label: "Greenhouse"},
  {id: "gupshup", label: "Gupshup"},
  {id: "github", label: "GitHub", planned: true},
  {id: "hubspot", label: "HubSpot"},
  {id: "intercom", label: "Intercom"},
  {id: "ironclad", label: "Ironclad"},
  {id: "mailchimp", label: "Mailchimp"},
  {id: "mailgun", label: "Mailgun"},
  {id: "outreach", label: "Outreach"},
  {id: "pipedrive", label: "Pipedrive"},
  {id: "razorpay", label: "Razorpay"},
  {id: "resend", label: "Resend"},
  {id: "sendgrid", label: "SendGrid"},
  {id: "shopify", label: "Shopify", planned: true},
  {id: "slack", label: "Slack"},
  {id: "statuspage", label: "Statuspage"},
  {id: "vanta", label: "Vanta"},
  {id: "zohobooks", label: "Zoho Books"},
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
