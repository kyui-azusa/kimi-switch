import {
  copyFileSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { basename, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const platforms = [
  { artifact: "kimiswitch-macos-aarch64", key: "darwin-aarch64", pattern: /\.app\.tar\.gz$/ },
  { artifact: "kimiswitch-macos-x86_64", key: "darwin-x86_64", pattern: /\.app\.tar\.gz$/ },
  { artifact: "kimiswitch-windows-x86_64", key: "windows-x86_64", pattern: /-setup\.exe$/ },
];

function walk(directory) {
  return readdirSync(directory).flatMap((entry) => {
    const path = join(directory, entry);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

function updaterSuffix(path) {
  if (path.endsWith(".app.tar.gz")) return ".app.tar.gz";
  if (path.endsWith("-setup.exe")) return "-setup.exe";
  throw new Error(`unsupported updater artifact: ${path}`);
}

export function buildManifest({ artifactsDirectory, outputDirectory, version, baseUrl, githubBaseUrl, notes, publishedAt = new Date() }) {
  const cleanVersion = version.replace(/^v/, "");
  if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(cleanVersion)) {
    throw new Error(`invalid release version: ${version}`);
  }
  if (!baseUrl) {
    throw new Error("update base URL is required");
  }
  for (const url of [baseUrl, githubBaseUrl].filter(Boolean)) {
    if (!url.startsWith("https://")) {
      throw new Error("update base URL must use HTTPS");
    }
  }

  mkdirSync(outputDirectory, { recursive: true });
  const platformEntries = {};

  for (const platform of platforms) {
    const artifactDirectory = join(artifactsDirectory, platform.artifact);
    const files = walk(artifactDirectory);
    const updater = files.find((path) => platform.pattern.test(path) && !path.endsWith(".sig"));
    if (!updater) throw new Error(`missing updater artifact for ${platform.key}`);
    const signaturePath = `${updater}.sig`;
    if (!files.includes(signaturePath)) throw new Error(`missing signature for ${platform.key}`);

    const updaterName = `KimiSwitch_${cleanVersion}_${platform.key}${updaterSuffix(updater)}`;
    const signatureName = `${updaterName}.sig`;
    copyFileSync(updater, join(outputDirectory, updaterName));
    copyFileSync(signaturePath, join(outputDirectory, signatureName));
    platformEntries[platform.key] = {
      signature: readFileSync(signaturePath, "utf8").trim(),
      updaterName,
    };

    for (const installer of files.filter((path) => path.endsWith(".dmg"))) {
      const installerName = `KimiSwitch_${cleanVersion}_${platform.key}.dmg`;
      copyFileSync(installer, join(outputDirectory, installerName));
    }
  }

  const renderManifest = (url) => ({
    version: cleanVersion,
    notes,
    pub_date: publishedAt.toISOString(),
    platforms: Object.fromEntries(
      Object.entries(platformEntries).map(([key, { signature, updaterName }]) => [
        key,
        { signature, url: `${url.replace(/\/$/, "")}/${updaterName}` },
      ]),
    ),
  });

  const manifest = renderManifest(baseUrl);
  const manifestPath = join(outputDirectory, "latest.json");
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  const result = { manifest, manifestPath };

  if (githubBaseUrl) {
    const githubManifest = renderManifest(githubBaseUrl);
    const githubManifestPath = join(outputDirectory, "latest.github.json");
    writeFileSync(githubManifestPath, `${JSON.stringify(githubManifest, null, 2)}\n`);
    result.githubManifest = githubManifest;
    result.githubManifestPath = githubManifestPath;
  }

  return result;
}

function parseArguments(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 2) {
    const name = argv[index]?.replace(/^--/, "");
    const value = argv[index + 1];
    if (!name || value === undefined) throw new Error(`invalid argument near ${argv[index] || "end"}`);
    options[name] = value;
  }
  return options;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const options = parseArguments(process.argv.slice(2));
  const result = buildManifest({
    artifactsDirectory: resolve(options.artifacts),
    outputDirectory: resolve(options.output),
    version: options.version,
    baseUrl: options["base-url"],
    githubBaseUrl: options["github-base-url"],
    notes: options.notes || `KimiSwitch ${options.version}`,
  });
  const manifests = [result.manifestPath, result.githubManifestPath].filter(Boolean).map((filePath) => basename(filePath));
  process.stdout.write(`${manifests.join(", ")}: ${Object.keys(result.manifest.platforms).join(", ")}\n`);
}
