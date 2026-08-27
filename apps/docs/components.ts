import {defineComponents} from "blume";

import IncludedIntegrations from "../../packages/docs-content/resources/environments/_components/IncludedIntegrations.astro";
import IntegrationCompatibility from "../../packages/docs-content/resources/integrations/_components/IntegrationCompatibility.astro";

export default defineComponents({
  mdx: {IncludedIntegrations, IntegrationCompatibility},
});
