import dayjs from "dayjs";
import { describe, expect, it } from "vitest";
import { customRuntimeLogWindow } from "./runtimeLogWindow";

const now = Date.parse("2026-10-03T14:00:00Z");
describe("custom runtime log bounds", () => {
  it("serializes a visible 24h UTC+8 range at second precision, including hidden milliseconds", () => {
    const result = customRuntimeLogWindow(
      [
        dayjs("2026-10-02T22:00:00.100+08:00"),
        dayjs("2026-10-03T22:00:00.900+08:00"),
      ],
      now,
    );
    expect(result).toEqual({
      bounds: {
        from: "2026-10-02T14:00:00.000Z",
        to: "2026-10-03T14:00:00.000Z",
      },
    });
  });
  it("rejects 24 hours plus one visible second instead of trimming the user's range", () => {
    expect(
      customRuntimeLogWindow([dayjs(now - 86400_000), dayjs(now + 1000)], now),
    ).toEqual({ error: "duration" });
  });
  it("checks both ordering and complete valid dates", () => {
    expect(customRuntimeLogWindow(null, now)).toEqual({ error: "required" });
    expect(customRuntimeLogWindow([dayjs("invalid"), dayjs(now)], now)).toEqual(
      { error: "required" },
    );
    expect(
      customRuntimeLogWindow([dayjs(now), dayjs(now - 1000)], now),
    ).toEqual({ error: "order" });
  });
  it("enforces the five-minute future limit at the request time", () => {
    const range: [dayjs.Dayjs, dayjs.Dayjs] = [
      dayjs(now),
      dayjs(now + 300_000),
    ];
    expect(customRuntimeLogWindow(range, now).error).toBeUndefined();
    expect(
      customRuntimeLogWindow([range[0], range[1].add(1, "second")], now),
    ).toEqual({ error: "future" });
    expect(customRuntimeLogWindow(range, now - 60_000)).toEqual({
      error: "future",
    });
  });
});
