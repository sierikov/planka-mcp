#!/usr/bin/env node
"use strict";

// Launcher for the planka-mcp binary, so that "npx -y @sierikov/planka-mcp"
// runs the server. The binary ships in one optional dependency per platform;
// npm installs only the one that matches this machine. Nothing here may write
// to stdout: it is the MCP channel.

const { spawnSync } = require("node:child_process");
const { constants } = require("node:os");

const PACKAGE = "@sierikov/planka-mcp";
// The platform packages this release ships, which our own package.json lists
// as optionalDependencies.
const PLATFORM_PACKAGES = require("./package.json").optionalDependencies || {};
const ELSEWHERE =
  'download the binary for your platform from https://github.com/sierikov/planka-mcp/releases/latest, or run ' +
  '"go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest"';

function noBuild(target) {
  console.error(`planka-mcp: no build for ${target}. Instead, ${ELSEWHERE}.`);
  process.exitCode = 1;
}

// The platform is supported, but npm did not install its package, usually
// because of --omit=optional or a lockfile made on another platform.
function missingPackage(target) {
  console.error(
    `planka-mcp: the package ${PACKAGE}-${target} is missing. Reinstall without --omit=optional, or ${ELSEWHERE}.`,
  );
  process.exitCode = 1;
}

function main() {
  const target = `${process.platform}-${process.arch}`;
  if (!Object.hasOwn(PLATFORM_PACKAGES, `${PACKAGE}-${target}`)) {
    return noBuild(target);
  }

  const exe = process.platform === "win32" ? "planka-mcp.exe" : "planka-mcp";
  let binary;
  try {
    binary = require.resolve(`${PACKAGE}-${target}/bin/${exe}`);
  } catch {
    return missingPackage(target);
  }

  const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit", shell: false });
  if (result.error) {
    console.error(`planka-mcp: cannot run ${binary}: ${result.error.code || result.error.message}`);
    process.exitCode = 1;
    return;
  }
  if (result.signal) {
    // Die from the same signal, so the caller sees what the child saw.
    process.kill(process.pid, result.signal);
    // Only reached if the signal did not end us: fall back to the shell's
    // convention for a death by signal.
    process.exitCode = 128 + (constants.signals[result.signal] || 0);
    return;
  }
  process.exitCode = result.status;
}

main();
