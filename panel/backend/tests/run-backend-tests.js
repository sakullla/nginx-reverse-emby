"use strict";

const { spawnSync } = require("node:child_process");
const path = require("node:path");

const testFiles = [
  "tests/property-roundtrip.test.js",
  "tests/property-isolation.test.js",
  "tests/property-revision.test.js",
  "tests/property-compatibility.test.js",
  "tests/http-rule-request-headers.test.js",
];

const accessViolationExitCodes = new Set([3221225477, -1073741819]);
const maxAttemptsPerFile = 3;

function runFile(testFile) {
  for (let attempt = 1; attempt <= maxAttemptsPerFile; attempt += 1) {
    const result = spawnSync(
      process.execPath,
      ["--test", "--test-isolation=none", testFile],
      {
        cwd: path.resolve(__dirname, ".."),
        stdio: "inherit",
      },
    );

    if (result.status === 0) {
      return 0;
    }

    if (
      !accessViolationExitCodes.has(result.status) ||
      attempt >= maxAttemptsPerFile
    ) {
      return result.status || 1;
    }

    console.warn(
      `[test-harness] ${testFile} crashed with exit code ${result.status}; retrying (${attempt + 1}/${maxAttemptsPerFile})`,
    );
  }

  return 1;
}

for (const testFile of testFiles) {
  const status = runFile(testFile);
  if (status !== 0) {
    process.exit(status);
  }
}

