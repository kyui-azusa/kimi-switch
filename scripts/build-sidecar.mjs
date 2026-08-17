import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const projectDirectory = join(scriptDirectory, "..");

const rustVersion = spawnSync("rustc", ["-vV"], {
  cwd: projectDirectory,
  encoding: "utf8",
});

if (rustVersion.status !== 0) {
  process.stderr.write(rustVersion.stderr || "Could not run rustc -vV.\n");
  process.exit(rustVersion.status ?? 1);
}

const hostTarget = rustVersion.stdout
  .split(/\r?\n/)
  .find((line) => line.startsWith("host: "))
  ?.slice("host: ".length)
  .trim();
const targetTriple =
  process.env.TAURI_ENV_TARGET_TRIPLE ||
  process.env.CARGO_BUILD_TARGET ||
  hostTarget;

if (!targetTriple) {
  process.stderr.write("Could not determine the Rust target triple.\n");
  process.exit(1);
}

const binaryDirectory = join(projectDirectory, "src-tauri", "binaries");
const executableSuffix = targetTriple.includes("windows") ? ".exe" : "";
const outputPath = join(
  binaryDirectory,
  `kimi-switch-proxy-${targetTriple}${executableSuffix}`,
);

mkdirSync(binaryDirectory, { recursive: true });

const result = spawnSync(
  "go",
  ["build", "-o", outputPath, "./cmd/kimi-switch"],
  { cwd: projectDirectory, stdio: "inherit" },
);

if (result.error) {
  process.stderr.write(`${result.error.message}\n`);
}

process.exit(result.status ?? 1);
