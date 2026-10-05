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
  {id: "ga4", label: "Google Analytics 4"},
  {id: "gainsight", label: "Gainsight"},
  {id: "gmail", label: "Gmail"},
  {id: "googledrive", label: "Google Drive"},
  {id: "greenhouse", label: "Greenhouse"},
  {id: "gupshup", label: "Gupshup"},
  {id: "github", label: "GitHub", planned: true},
  {id: "hubspot", label: "HubSpot"},
  {id: "impact", label: "impact.com"},
  {id: "intercom", label: "Intercom"},
  {id: "ironclad", label: "Ironclad"},
  {id: "jsm", label: "Jira Service Management"},
  {id: "mailchimp", label: "Mailchimp"},
  {id: "mailgun", label: "Mailgun"},
  {id: "okta", label: "Okta"},
  {id: "outreach", label: "Outreach"},
  {id: "pipedrive", label: "Pipedrive"},
  {id: "razorpay", label: "Razorpay"},
  {id: "redshift", label: "Amazon Redshift"},
  {id: "resend", label: "Resend"},
  {id: "sendgrid", label: "SendGrid"},
  {id: "shiprocket", label: "Shiprocket"},
  {id: "shopify", label: "Shopify"},
  {id: "slack", label: "Slack"},
  {id: "statuspage", label: "Statuspage"},
  {id: "vanta", label: "Vanta"},
  {id: "zohobooks", label: "Zoho Books"},
  {id: "zohoinventory", label: "Zoho Inventory"},
];

// Local marks in apps/docs/public/integrations. Most are the Composio
// logo-cdn asset. These resource ids use a different toolkit slug:
// digitalocean is digital_ocean, ga4 is google_analytics, gainsight is
// gainsight_px, jsm is jira, and redshift is amazon_redshift. Zoho Books and
// Zoho Inventory use the distinct product marks from Composio's open-logos
// set; the logo-cdn files for those two slugs are the same Zoho square.
// Carta, Gupshup, impact.com, Ironclad, and Shiprocket are not in either
// set; their files are the vendor marks.
const logoExtension: Record<string, string> = {
  shiprocket: "png",
};

export function composioLogo(id: string, _theme?: "dark"): string {
  return `/integrations/${id}.${logoExtension[id] ?? "svg"}`;
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
