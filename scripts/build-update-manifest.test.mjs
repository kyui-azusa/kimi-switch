import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { mkdtemp } from "node:fs/promises";

import { buildManifest } from "./build-update-manifest.mjs";

test("builds a complete signed manifest for all supported platforms", async () => {
  const root = await mkdtemp(join(tmpdir(), "kimiswitch-manifest-"));
  const artifacts = join(root, "artifacts");
  const output = join(root, "output");
  const fixtures = [
    ["kimiswitch-macos-aarch64", "KimiSwitch.app.tar.gz", "arm-signature"],
    ["kimiswitch-macos-x86_64", "KimiSwitch.app.tar.gz", "intel-signature"],
    ["kimiswitch-windows-x86_64", "KimiSwitch_0.2.0_x64-setup.exe", "windows-signature"],
  ];
  for (const [artifact, file, signature] of fixtures) {
    const directory = join(artifacts, artifact);
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, file), `${artifact}-content`);
    writeFileSync(join(directory, `${file}.sig`), signature);
  }

  const { manifest, manifestPath } = buildManifest({
    artifactsDirectory: artifacts,
    outputDirectory: output,
    version: "v0.2.0",
    baseUrl: "https://github.com/kyui-azusa/kimi-switch/releases/download/v0.2.0",
    notes: "Updater release",
    publishedAt: new Date("2026-08-18T00:00:00Z"),
  });

  assert.equal(manifest.version, "0.2.0");
  assert.deepEqual(Object.keys(manifest.platforms).sort(), ["darwin-aarch64", "darwin-x86_64", "windows-x86_64"]);
  assert.equal(manifest.platforms["darwin-aarch64"].signature, "arm-signature");
  assert.match(manifest.platforms["windows-x86_64"].url, /KimiSwitch_0\.2\.0_windows-x86_64-setup\.exe$/);
  assert.deepEqual(JSON.parse(readFileSync(manifestPath, "utf8")), manifest);
});

test("rejects an incomplete release before latest.json is written", async () => {
  const root = await mkdtemp(join(tmpdir(), "kimiswitch-manifest-incomplete-"));
  assert.throws(() => buildManifest({
    artifactsDirectory: root,
    outputDirectory: join(root, "output"),
    version: "0.2.0",
    baseUrl: "https://updates.example.com/kimi-switch/v0.2.0",
    notes: "Incomplete",
  }), /missing updater artifact|ENOENT/);
});

test("writes a GitHub fallback manifest with rebased URLs", async () => {
  const root = await mkdtemp(join(tmpdir(), "kimiswitch-manifest-github-"));
  const artifacts = join(root, "artifacts");
  const output = join(root, "output");
  const fixtures = [
    ["kimiswitch-macos-aarch64", "KimiSwitch.app.tar.gz", "arm-signature"],
    ["kimiswitch-macos-x86_64", "KimiSwitch.app.tar.gz", "intel-signature"],
    ["kimiswitch-windows-x86_64", "KimiSwitch_0.2.0_x64-setup.exe", "windows-signature"],
  ];
  for (const [artifact, file, signature] of fixtures) {
    const directory = join(artifacts, artifact);
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, file), `${artifact}-content`);
    writeFileSync(join(directory, `${file}.sig`), signature);
  }

  const { manifest, manifestPath, githubManifest, githubManifestPath } = buildManifest({
    artifactsDirectory: artifacts,
    outputDirectory: output,
    version: "v0.2.0",
    baseUrl: "https://updates.example.com/kimi-switch/releases/v0.2.0",
    githubBaseUrl: "https://github.com/kyui-azusa/kimi-switch/releases/download/v0.2.0/",
    notes: "Updater release",
    publishedAt: new Date("2026-08-18T00:00:00Z"),
  });

  assert.match(manifest.platforms["darwin-aarch64"].url, /^https:\/\/updates\.example\.com\/kimi-switch\/releases\/v0\.2\.0\//);
  assert.equal(githubManifest.version, manifest.version);
  assert.equal(githubManifest.pub_date, manifest.pub_date);
  assert.equal(githubManifest.platforms["darwin-aarch64"].signature, "arm-signature");
  assert.match(githubManifest.platforms["darwin-aarch64"].url, /^https:\/\/github\.com\/kyui-azusa\/kimi-switch\/releases\/download\/v0\.2\.0\/KimiSwitch_0\.2\.0_darwin-aarch64\.app\.tar\.gz$/);
  assert.match(githubManifest.platforms["windows-x86_64"].url, /KimiSwitch_0\.2\.0_windows-x86_64-setup\.exe$/);
  assert.deepEqual(JSON.parse(readFileSync(githubManifestPath, "utf8")), githubManifest);
  assert.deepEqual(JSON.parse(readFileSync(manifestPath, "utf8")), manifest);
});
