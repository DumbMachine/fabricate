import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  site: process.env.PUBLIC_FABRICATE_SITE_URL || "https://fabricate.dmach.in",
  output: "static",
  vite: {
    plugins: [tailwindcss()],
  },
});
