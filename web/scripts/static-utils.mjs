import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

export async function fileMap(root) {
  const files = new Map();

  async function walk(relativeDir) {
    const absoluteDir = path.join(root, relativeDir);
    const entries = await readdir(absoluteDir, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of entries) {
      if (entry.name === ".DS_Store") continue;
      const relativePath = path.join(relativeDir, entry.name);
      if (entry.isDirectory()) {
        await walk(relativePath);
      } else if (entry.isFile()) {
        const content = await readFile(path.join(root, relativePath));
        files.set(
          relativePath.split(path.sep).join("/"),
          createHash("sha256").update(content).digest("hex"),
        );
      }
    }
  }

  await walk("");
  return files;
}

export function compareFileMaps(expected, actual) {
  const differences = [];
  const names = new Set([...expected.keys(), ...actual.keys()]);
  for (const name of [...names].sort()) {
    if (!expected.has(name)) differences.push(`仅生成物存在: ${name}`);
    else if (!actual.has(name)) differences.push(`缺少生成物: ${name}`);
    else if (expected.get(name) !== actual.get(name)) differences.push(`内容漂移: ${name}`);
  }
  return differences;
}
