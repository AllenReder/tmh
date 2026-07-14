import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const forwardedSignals = ["SIGINT", "SIGTERM", "SIGHUP"];

const platforms = new Map([
  ["darwin", "darwin"],
  ["linux", "linux"]
]);

const architectures = new Map([
  ["x64", "amd64"],
  ["arm64", "arm64"]
]);

export function resolveBinary({
  platform = process.platform,
  arch = process.arch,
  root = packageRoot
} = {}) {
  const releasePlatform = platforms.get(platform);
  const releaseArch = architectures.get(arch);
  if (!releasePlatform || !releaseArch) {
    throw new Error(
      `unsupported platform: ${platform}/${arch}; supported platforms are darwin and linux on x64 or arm64`
    );
  }
  return path.join(root, "vendor", `${releasePlatform}-${releaseArch}`, "tmh");
}

export function invocation({
  command,
  args = process.argv.slice(2),
  platform = process.platform,
  arch = process.arch,
  root = packageRoot
}) {
  if (command !== "tmh" && command !== "tmha") {
    throw new Error(`unsupported command name: ${command}`);
  }
  return {
    binary: resolveBinary({ platform, arch, root }),
    args: [...args],
    options: {
      argv0: command,
      stdio: "inherit"
    }
  };
}

export function launch({
  command,
  args = process.argv.slice(2),
  platform = process.platform,
  arch = process.arch,
  root = packageRoot,
  spawnImpl = spawn,
  parent = process
}) {
  const spec = invocation({ command, args, platform, arch, root });
  const child = spawnImpl(spec.binary, spec.args, spec.options);
  const signalHandlers = new Map();

  const cleanup = () => {
    for (const [signal, handler] of signalHandlers) {
      parent.removeListener(signal, handler);
    }
  };

  for (const signal of forwardedSignals) {
    const handler = () => {
      if (!child.killed) {
        child.kill(signal);
      }
    };
    signalHandlers.set(signal, handler);
    parent.on(signal, handler);
  }

  child.once("error", error => {
    cleanup();
    parent.stderr.write(`tmh npm launcher: failed to start ${spec.binary}: ${error.message}\n`);
    parent.exitCode = 1;
  });

  child.once("exit", (code, signal) => {
    cleanup();
    if (signal) {
      parent.kill(parent.pid, signal);
      return;
    }
    parent.exitCode = code ?? 1;
  });

  return child;
}
