//  @ts-check

import { tanstackConfig } from "@tanstack/eslint-config"

export default [
  ...tanstackConfig,
  {
    rules: {
      "import/no-cycle": "off",
      "import/order": "off",
      "import/consistent-type-specifier-style": "off",
      "sort-imports": "off",
      "@typescript-eslint/array-type": "off",
      "@typescript-eslint/require-await": "off",
      "pnpm/json-enforce-catalog": "off",
    },
  },
  {
    files: [
      "src/components/motion/**/*.{ts,tsx}",
      "src/lib/ease.ts",
      "src/lib/hooks/**/*.{ts,tsx}",
      "src/lib/presence-gate.tsx",
    ],
    rules: {
      "@typescript-eslint/naming-convention": "off",
      "@typescript-eslint/no-unnecessary-condition": "off",
      "@typescript-eslint/no-unnecessary-type-assertion": "off",
      "no-shadow": "off",
    },
  },
  {
    ignores: ["eslint.config.js", ".prettierrc", "src/routeTree.gen.ts"],
  },
]
