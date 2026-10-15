import { cpSync, existsSync, rmSync } from "node:fs"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const candidates = [
  resolve(appRoot, "dist/client"),
  resolve(appRoot, ".output/public"),
  resolve(appRoot, "dist"),
]
const source = candidates.find((dir) => existsSync(resolve(dir, "index.html")))
if (!source) {
  console.error("console build did not produce index.html")
  process.exit(1)
}

const target = resolve(appRoot, "../../console/ui")
rmSync(target, { recursive: true, force: true })
cpSync(source, target, { recursive: true })
console.log(`synced ${source} -> ${target}`)
