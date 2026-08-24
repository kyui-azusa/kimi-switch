import { existsSync, mkdirSync, mkdtempSync, renameSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { execFileSync } from "node:child_process";

const input = process.argv[2];
if (!input) throw new Error("usage: node scripts/repair-dmg-presentation.mjs <path.dmg>");
if (process.platform !== "darwin") throw new Error("DMG presentation repair requires macOS");

const dmgPath = resolve(input);
if (!existsSync(dmgPath)) throw new Error(`DMG not found: ${dmgPath}`);

const workDirectory = mkdtempSync(join(tmpdir(), "kimiswitch-dmg-layout-"));
const readWritePath = join(workDirectory, "layout-rw.dmg");
const repairedPath = join(workDirectory, "layout-repaired.dmg");
const mountDirectory = join(workDirectory, "mount");
let device = "";

function run(command, args, options = {}) {
  return execFileSync(command, args, { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], ...options });
}

function detachDisk() {
  for (const delaySeconds of [0, 1, 2, 4, 8]) {
    if (delaySeconds) run("sleep", [String(delaySeconds)]);
    try {
      run("hdiutil", ["detach", device], { stdio: ["ignore", "pipe", "pipe"] });
      device = "";
      return;
    } catch (error) {
      if (error?.status !== 16) throw error;
    }
  }

  run("hdiutil", ["detach", device]);
  device = "";
}

function writeLayout(volumeName) {
  const appleScript = String.raw`
on run argv
  set volumeName to item 1 of argv

  tell application "Finder"
    tell disk (volumeName as string)
      open
      tell container window
        set current view to icon view
        set toolbar visible to false
        set statusbar visible to false
        set the bounds to {10, 620, 670, 1020}
      end tell

      set viewOptions to the icon view options of container window
      tell viewOptions
        set icon size to 128
        set text size to 16
        set arrangement to not arranged
      end tell
      set background picture of viewOptions to file ".background:background.png"

      set position of item "KimiSwitch.app" to {170, 205}
      set position of item "Applications" to {490, 205}
      close
      open
      delay 1
      close
    end tell
  end tell
end run
`;

  run("/usr/bin/osascript", ["-", volumeName], {
    input: appleScript,
    stdio: ["pipe", "pipe", "inherit"],
  });
}

try {
  run("hdiutil", ["convert", dmgPath, "-format", "UDRW", "-o", readWritePath]);
  mkdirSync(mountDirectory);
  const attachOutput = run("hdiutil", ["attach", "-readwrite", "-nobrowse", "-mountpoint", mountDirectory, readWritePath]);
  const mountedLine = attachOutput.split("\n").find((line) => line.includes(mountDirectory));
  device = mountedLine?.match(/^\/dev\/\S+/)?.[0] || "";
  if (!device) throw new Error(`could not determine mounted device for ${basename(dmgPath)}`);

  run("sleep", ["2"]);

  const backgroundPath = join(mountDirectory, ".background", "background.png");
  if (!existsSync(backgroundPath)) throw new Error(`DMG background not found: ${backgroundPath}`);
  const storePath = join(mountDirectory, ".DS_Store");
  rmSync(storePath, { force: true });
  writeLayout(basename(mountDirectory));

  for (let attempt = 0; attempt < 20 && !existsSync(storePath); attempt += 1) {
    run("sleep", ["1"]);
  }
  if (!existsSync(storePath)) throw new Error("Finder did not write the DMG .DS_Store layout");
  run("sync", []);

  detachDisk();
  run("hdiutil", ["convert", readWritePath, "-format", "UDZO", "-imagekey", "zlib-level=9", "-o", repairedPath]);
  renameSync(repairedPath, dmgPath);
  process.stdout.write(`Repaired DMG presentation: ${dmgPath}\n`);
} finally {
  if (device) {
    try {
      detachDisk();
    } catch {
      // Preserve the original failure while still attempting to detach.
    }
  }
  rmSync(workDirectory, { recursive: true, force: true });
}
