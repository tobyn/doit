// Copies manual/*.md into docs/, rewriting relative .md links for VitePress.
import { readdirSync, readFileSync, writeFileSync, mkdirSync, unlinkSync } from "fs";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const manualDir = resolve(__dirname, "../../manual");
const docsDir = resolve(__dirname, "../docs");

mkdirSync(docsDir, { recursive: true });

// Clean old synced files.
for (const f of readdirSync(docsDir)) {
  if (f.endsWith(".md")) unlinkSync(resolve(docsDir, f));
}

for (const f of readdirSync(manualDir)) {
  if (!f.endsWith(".md")) continue;
  let content = readFileSync(resolve(manualDir, f), "utf-8");

  // Rewrite relative .md links: [text](foo.md) → [text](foo)
  // but leave external URLs and anchors alone.
  content = content.replace(
    /\[([^\]]*)\]\((?!https?:\/\/)([^)]*?)\.md(#[^)]*)?\)/g,
    (_, text, file, anchor) => `[${text}](${file}${anchor || ""})`
  );

  writeFileSync(resolve(docsDir, f), content);
}

console.log(`Synced ${readdirSync(docsDir).filter(f => f.endsWith(".md")).length} docs from manual/`);
