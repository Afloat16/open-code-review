// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const assert = require("assert");

const {
  parseVersionOutput,
  semverGt,
  shouldShowUpdateHint,
} = require("./version");

assert.strictEqual(
  parseVersionOutput("open-code-review v1.8.6 (1b193db35) darwin/arm64"),
  "1.8.6"
);
assert.strictEqual(parseVersionOutput("version unavailable"), null);
assert.strictEqual(
  parseVersionOutput("open-code-review v1.8.6-beta.1+darwin.arm64"),
  "1.8.6-beta.1+darwin.arm64"
);

assert.strictEqual(semverGt("1.8.7", "1.8.6"), true);
assert.strictEqual(semverGt("1.8.6", "1.8.6"), false);
assert.strictEqual(semverGt("1.8.5", "1.8.6"), false);
assert.strictEqual(semverGt("1.8.6", "1.8.6-beta.1"), true);
assert.strictEqual(semverGt("1.8.6+build.2", "1.8.6+build.1"), false);
assert.strictEqual(semverGt("1.8.6", "1.8.6-beta.1+build.1"), true);
assert.strictEqual(semverGt("not-a-version", "1.8.6"), false);

// Compare prerelease identifiers using SemVer precedence, not string order.
assert.strictEqual(semverGt("1.8.6-beta.2", "1.8.6-beta.1"), true);
assert.strictEqual(semverGt("1.8.6-beta.10", "1.8.6-beta.2"), true);
assert.strictEqual(semverGt("1.8.6-beta.2", "1.8.6-beta.10"), false);
assert.strictEqual(semverGt("1.8.6-rc.1", "1.8.6-beta.9"), true);
assert.strictEqual(semverGt("1.8.6-alpha.beta", "1.8.6-alpha.1"), true);
assert.strictEqual(semverGt("1.8.6-alpha.1", "1.8.6-alpha"), true);
assert.strictEqual(semverGt("1.8.6-alpha", "1.8.6-alpha.1"), false);
assert.strictEqual(semverGt("1.8.6-rc.1+new", "1.8.6-rc.1+old"), false);
assert.strictEqual(semverGt("1.8.6-rc.1", "1.8.6"), false);
assert.strictEqual(semverGt("1.8.6-9007199254740993", "1.8.6-9007199254740992"), true);
assert.strictEqual(shouldShowUpdateHint("1.8.6-beta.2", "1.8.6-beta.1"), true);
assert.strictEqual(shouldShowUpdateHint("1.8.6-beta.1", "1.8.6-beta.2"), false);

assert.strictEqual(shouldShowUpdateHint("1.8.7", "1.8.6"), true);
assert.strictEqual(shouldShowUpdateHint("1.8.6", "1.8.6"), false);
assert.strictEqual(shouldShowUpdateHint("1.8.5", "1.8.6"), false);
assert.strictEqual(shouldShowUpdateHint("not-a-version", "1.8.6"), false);
assert.strictEqual(shouldShowUpdateHint("1.8.7", null), false);
assert.strictEqual(shouldShowUpdateHint("1.8.7", "not-a-version"), false);
assert.strictEqual(shouldShowUpdateHint("1.8.6+new", "1.8.6+old"), false);

console.log("version tests passed");
