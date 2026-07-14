#!/usr/bin/env node

import { launch } from "../lib/launcher.mjs";

try {
  launch({ command: "tmh" });
} catch (error) {
  process.stderr.write(`tmh npm launcher: ${error.message}\n`);
  process.exitCode = 1;
}
