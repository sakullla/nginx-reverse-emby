"use strict";

const fc = require("fast-check");

const SQLITE_TARGET = ":memory:";
const safeString = fc.string({ maxLength: 50 }).map((s) => s.replace(/\0/g, ""));
const nonEmptyString = fc.string({ minLength: 1, maxLength: 50 }).map((s) => s.replace(/\0/g, ""));

function detectSqliteAvailability() {
  try {
    const storage = require("../storage-sqlite");
    storage.init(SQLITE_TARGET);
    storage.close();
    return true;
  } catch (_) {
    return false;
  }
}

function loadFreshStorage(modulePath, initArg) {
  const resolved = require.resolve(modulePath);
  delete require.cache[resolved];
  const storage = require(modulePath);
  if (initArg !== undefined) {
    storage.init(initArg);
  }
  return storage;
}

function closeQuietly(storage) {
  try {
    storage.close();
  } catch (_) {
    // ignore test teardown noise
  }
}

function dedupById(items, key = "id") {
  const map = new Map();
  for (const item of items) {
    map.set(item[key], item);
  }
  return [...map.values()];
}

function getNumRuns(suiteName, fallback) {
  const suiteKey = `PANEL_BACKEND_TEST_NUM_RUNS_${String(suiteName).toUpperCase()}`;
  const raw = process.env[suiteKey] || process.env.PANEL_BACKEND_TEST_NUM_RUNS;
  const parsed = Number.parseInt(raw, 10);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
}

module.exports = {
  SQLITE_TARGET,
  canRunSqlite: detectSqliteAvailability(),
  safeString,
  nonEmptyString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
};
