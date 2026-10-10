import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { defaultConsoleThemes } from "./defaultConsoleThemes";

describe("default console themes", () => {
  it.each(defaultConsoleThemes.map((theme) => [theme.id, theme] as const))(
    "%s mirrors the server-embedded Theme Package",
    (id, theme) => {
      const embedded = JSON.parse(
        readFileSync(
          // Vitest runs from console/; the packages ship with the Gateway.
          resolve(`../internal/consoletheme/builtin/${id}.theme.json`),
          "utf8",
        ),
      );
      expect(theme).toEqual(embedded);
    },
  );
});
