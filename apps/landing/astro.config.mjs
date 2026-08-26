import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  site: "https://fabricate.dmach.in",
  output: "static",
  vite: {
    plugins: [tailwindcss()],
  },
});
