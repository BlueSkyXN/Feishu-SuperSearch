import path from "node:path";
import { fileURLToPath } from "node:url";
import { compareFileMaps, fileMap } from "./static-utils.mjs";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dist = path.join(webRoot, "dist");
const embedded = path.resolve(webRoot, "../transport/httpapi/static");

try {
  const differences = compareFileMaps(await fileMap(dist), await fileMap(embedded));
  if (differences.length > 0) {
    console.error("Go embed 产物与 web/dist 不一致：");
    for (const difference of differences) console.error(`- ${difference}`);
    console.error("运行 `npm run build` 重新生成产物。");
    process.exitCode = 1;
  } else {
    console.log("Go embed 产物与 web/dist 完全一致。");
  }
} catch (error) {
  console.error(`无法检查生成物: ${error.message}`);
  process.exitCode = 1;
}
