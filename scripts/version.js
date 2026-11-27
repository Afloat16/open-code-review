// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const SEMVER_RE =
  /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;

function parseVersionOutput(output) {
  const match = String(output || "").match(
    /v(\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)/
  );
  return match ? match[1] : null;
}

function semverGt(a, b) {
  if (!SEMVER_RE.test(a) || !SEMVER_RE.test(b)) return false;

  const aWithoutBuild = a.replace(/\+.*$/, "");
  const bWithoutBuild = b.replace(/\+.*$/, "");
  const pa = aWithoutBuild.replace(/-.*$/, "").split(".").map(Number);
  const pb = bWithoutBuild.replace(/-.*$/, "").split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (pa[i] > pb[i]) return true;
    if (pa[i] < pb[i]) return false;
  }
  const aPre = aWithoutBuild.indexOf("-");
  const bPre = bWithoutBuild.indexOf("-");
  if (aPre === -1 || bPre === -1) return aPre === -1 && bPre !== -1;

  const aIds = aWithoutBuild.slice(aPre + 1).split(".");
  const bIds = bWithoutBuild.slice(bPre + 1).split(".");
  for (let i = 0; i < Math.min(aIds.length, bIds.length); i++) {
    const x = aIds[i];
    const y = bIds[i];
    if (x === y) continue;
    const xNumeric = /^\d+$/.test(x);
    const yNumeric = /^\d+$/.test(y);
    if (xNumeric && yNumeric) {
      const xn = BigInt(x);
      const yn = BigInt(y);
      if (xn !== yn) return xn > yn;
      continue;
    }
    if (xNumeric !== yNumeric) return !xNumeric;
    return x > y;
  }
  return aIds.length > bIds.length;
}

function shouldShowUpdateHint(hintVersion, installedVersion) {
  if (!SEMVER_RE.test(hintVersion) || !SEMVER_RE.test(installedVersion)) {
    return false;
  }
  return semverGt(hintVersion, installedVersion);
}

module.exports = {
  SEMVER_RE,
  parseVersionOutput,
  semverGt,
  shouldShowUpdateHint,
};
