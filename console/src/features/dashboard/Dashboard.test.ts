import { describe, expect, it } from "vitest";
import type { OverviewRepositoryStatistics } from "../../client";
import { sortRepositoryStatistics } from "./Dashboard";

function row(
  name: string,
  oneDay: number,
  sevenDays: number,
): OverviewRepositoryStatistics {
  return {
    repositoryId: name,
    name,
    format: "raw",
    requests: { oneDay, sevenDays, thirtyDays: sevenDays },
    denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
    objectCount: 0,
    usedBytes: 0,
  };
}

describe("sortRepositoryStatistics", () => {
  it("orders by the selected window and leaves the API snapshot unchanged", () => {
    const rows = [row("zeta", 5, 0), row("alpha", 0, 8)];

    expect(
      sortRepositoryStatistics(rows, "oneDay").map((item) => item.name),
    ).toEqual(["zeta", "alpha"]);
    expect(
      sortRepositoryStatistics(rows, "sevenDays").map((item) => item.name),
    ).toEqual(["alpha", "zeta"]);
    expect(rows.map((item) => item.name)).toEqual(["zeta", "alpha"]);
  });
});
