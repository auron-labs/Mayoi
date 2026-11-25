import { languages, process, workspace, type Document } from "mayoi";

// A folder set up for Biome or Prettier is formatted with it, as the
// people who work in it do, since oxfmt reads neither's settings; any
// other with oxfmt, which reads .oxfmtrc.json.
const prettierFiles = [
  ".prettierrc", ".prettierrc.json", ".prettierrc.json5", ".prettierrc.yaml", ".prettierrc.yml", ".prettierrc.toml",
  ".prettierrc.js", ".prettierrc.cjs", ".prettierrc.mjs", ".prettierrc.ts", ".prettierrc.cts", ".prettierrc.mts",
  "prettier.config.js", "prettier.config.cjs", "prettier.config.mjs", "prettier.config.ts", "prettier.config.cts",
  "prettier.config.mts",
];

async function has(files: string[]) {
  const stats = await Promise.all(files.map((f) => workspace.stat(f)));
  return stats.some((s) => s && !s.isDir);
}

async function prettierInPackage() {
  try {
    return "prettier" in JSON.parse(await workspace.readFile("package.json"));
  } catch {
    return false;
  }
}

// formatter is the tool the folder formats with, and its arguments for
// the text of a file at rel on stdin.
async function formatter(rel: string): Promise<[string, string[]]> {
  const [oxfmt, biome, prettier, inPackage] = await Promise.all([
    has([".oxfmtrc.json", ".oxfmtrc.jsonc"]),
    has(["biome.json", "biome.jsonc"]),
    has(prettierFiles),
    prettierInPackage(),
  ]);
  if (!oxfmt && biome) return ["biome", ["format", `--stdin-file-path=${rel}`]];
  if (!oxfmt && (prettier || inPackage)) return ["prettier", ["--stdin-filepath", rel]];
  return ["oxfmt", [`--stdin-filepath=${rel}`]];
}

// program is the folder's own copy of a tool, in node_modules, before
// the one on the path; .cmd is Windows'.
async function program(tool: string) {
  const local = `node_modules/.bin/${tool}`;
  if (await workspace.stat(`${local}.cmd`)) return `${local}.cmd`;
  return (await workspace.stat(local)) ? local : tool;
}

// relative is the document's path from the folder, with slashes, which
// tells the tool how to parse it; its name for one outside it.
async function relative(doc: Document) {
  const root = (await workspace.root()).replaceAll("\\", "/").replace(/\/$/, "") + "/";
  const path = doc.path.replaceAll("\\", "/");
  return path.startsWith(root) ? path.slice(root.length) : doc.name;
}

languages.registerFormatter("oxfmt.format", async (doc) => {
  const [tool, args] = await formatter(await relative(doc));
  const missing = `${tool} isn't installed: ${tool === "oxfmt" ? "npm install -g oxfmt" : "npm install in the folder"}`;
  let run;
  try {
    run = await process.run(await program(tool), args, { input: await doc.text() });
  } catch (err) {
    throw /not found/.test(String(err)) ? new Error(missing) : err;
  }
  if (run.code === 127) throw new Error(missing);
  if (run.code !== 0) {
    throw new Error(run.stderr.split("\n").map((l) => l.trim()).find(Boolean) ?? `${tool} ended with ${run.code}`);
  }
  return run.stdout;
});
