import { defineConfig } from "vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

const apiTarget = process.env.FAB_CONSOLE_DEV_API || "http://127.0.0.1:4173"

const config = defineConfig({
  resolve: { tsconfigPaths: true },
  server: {
    proxy: {
      "/api": apiTarget,
      "/healthz": apiTarget,
      "/.well-known": apiTarget,
      "/s/": apiTarget,
    },
  },
  preview: { host: "127.0.0.1" },
  plugins: [
    tailwindcss(),
    tanstackStart({
      spa: {
        enabled: true,
        maskPath: "/",
        prerender: {
          enabled: true,
          outputPath: "/index.html",
        },
      },
    }),
    viteReact(),
  ],
})

export default config
