"use strict";
const BACKEND = process.env.PANEL_STORAGE_BACKEND || "sqlite";

let impl;
if (BACKEND === "json") {
  impl = require("./storage-json");
} else {
  impl = require("./storage-sqlite");
}

module.exports = impl;
