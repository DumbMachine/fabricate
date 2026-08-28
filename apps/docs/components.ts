import {defineComponents} from "blume";

import CommandOutput from "../../packages/docs-content/resources/_components/CommandOutput.astro";
import IncludedIntegrations from "../../packages/docs-content/resources/environments/_components/IncludedIntegrations.astro";
import IntegrationCompatibility from "../../packages/docs-content/resources/_components/IntegrationCompatibility.astro";
import ResourceScenarios from "../../packages/docs-content/resources/_components/ResourceScenarios.astro";
import SupportedOperations from "../../packages/docs-content/resources/_components/SupportedOperations.astro";

export default defineComponents({
  mdx: {CommandOutput, IncludedIntegrations, IntegrationCompatibility, ResourceScenarios, SupportedOperations},
});
