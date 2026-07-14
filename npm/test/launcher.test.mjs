import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import path from "node:path";
import test from "node:test";

import { invocation, launch, resolveBinary } from "../lib/launcher.mjs";

const root = path.join(path.sep, "package");

test("maps every supported release platform", () => {
  assert.equal(resolveBinary({ platform: "darwin", arch: "x64", root }), path.join(root, "vendor", "darwin-amd64", "tmh"));
  assert.equal(resolveBinary({ platform: "darwin", arch: "arm64", root }), path.join(root, "vendor", "darwin-arm64", "tmh"));
  assert.equal(resolveBinary({ platform: "linux", arch: "x64", root }), path.join(root, "vendor", "linux-amd64", "tmh"));
  assert.equal(resolveBinary({ platform: "linux", arch: "arm64", root }), path.join(root, "vendor", "linux-arm64", "tmh"));
});

test("rejects unsupported platforms and architectures", () => {
  assert.throws(() => resolveBinary({ platform: "win32", arch: "x64", root }), /unsupported platform: win32\/x64/);
  assert.throws(() => resolveBinary({ platform: "linux", arch: "riscv64", root }), /unsupported platform: linux\/riscv64/);
});

test("preserves command name, arguments, and inherited stdio", () => {
  const spec = invocation({ command: "tmha", args: ["config", "show"], platform: "linux", arch: "x64", root });
  assert.deepEqual(spec.args, ["config", "show"]);
  assert.equal(spec.options.argv0, "tmha");
  assert.equal(spec.options.stdio, "inherit");
});

test("forwards signals and child exit codes", () => {
  const parent = new EventEmitter();
  parent.pid = 42;
  parent.stderr = { write() {} };
  parent.killCalls = [];
  parent.kill = (pid, signal) => parent.killCalls.push([pid, signal]);

  const child = new EventEmitter();
  child.killed = false;
  child.killCalls = [];
  child.kill = signal => {
    child.killCalls.push(signal);
    return true;
  };

  const calls = [];
  launch({
    command: "tmh",
    args: ["version"],
    platform: "linux",
    arch: "x64",
    root,
    parent,
    spawnImpl: (...args) => {
      calls.push(args);
      return child;
    }
  });

  assert.equal(calls.length, 1);
  parent.emit("SIGTERM");
  assert.deepEqual(child.killCalls, ["SIGTERM"]);
  child.emit("exit", 7, null);
  assert.equal(parent.exitCode, 7);
  assert.equal(parent.listenerCount("SIGTERM"), 0);
});

test("re-raises a terminating child signal after cleanup", () => {
  const parent = new EventEmitter();
  parent.pid = 42;
  parent.stderr = { write() {} };
  parent.killCalls = [];
  parent.kill = (pid, signal) => parent.killCalls.push([pid, signal]);

  const child = new EventEmitter();
  child.killed = false;
  child.kill = () => true;

  launch({
    command: "tmha",
    platform: "darwin",
    arch: "arm64",
    root,
    parent,
    spawnImpl: () => child
  });
  child.emit("exit", null, "SIGINT");

  assert.deepEqual(parent.killCalls, [[42, "SIGINT"]]);
  assert.equal(parent.listenerCount("SIGINT"), 0);
});

test("reports child startup failures", () => {
  let stderr = "";
  const parent = new EventEmitter();
  parent.pid = 42;
  parent.stderr = { write: value => { stderr += value; } };
  parent.kill = () => {};

  const child = new EventEmitter();
  child.killed = false;
  child.kill = () => true;

  launch({
    command: "tmh",
    platform: "linux",
    arch: "x64",
    root,
    parent,
    spawnImpl: () => child
  });
  child.emit("error", new Error("permission denied"));

  assert.equal(parent.exitCode, 1);
  assert.match(stderr, /failed to start .*permission denied/);
});
