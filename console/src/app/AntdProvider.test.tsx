import { DatePicker } from "antd";
import dayjs from "dayjs";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it } from "vitest";
import { PreferencesProvider, usePreferences } from "../lib/preferences";
import { AntdProvider } from "./AntdProvider";

function Calendar() {
  const { setLocale } = usePreferences();
  return (
    <>
      <button onClick={() => setLocale("en-US")}>English</button>
      <button onClick={() => setLocale("zh-CN")}>Chinese</button>
      <DatePicker defaultValue={dayjs("2026-10-14")} />
    </>
  );
}

afterEach(() => {
  cleanup();
  localStorage.clear();
});

it("switches the open calendar's month and weekdays with the Console language", async () => {
  localStorage.setItem("ag.console.locale", "zh-CN");
  const user = userEvent.setup();
  render(
    <PreferencesProvider>
      <AntdProvider>
        <Calendar />
      </AntdProvider>
    </PreferencesProvider>,
  );
  const weekdays = () =>
    [...document.querySelectorAll(".ant-picker-panel th")].map(
      (node) => node.textContent,
    );
  await user.click(screen.getByRole("textbox"));
  expect(weekdays()).toEqual(["一", "二", "三", "四", "五", "六", "日"]);
  expect(screen.getByRole("button", { name: "选择月份" })).toHaveTextContent(
    "10月",
  );

  await user.click(screen.getByRole("button", { name: "English" }));
  await user.click(screen.getByRole("textbox"));
  expect(weekdays()).toEqual(["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"]);
  expect(
    screen.getByRole("button", { name: "Choose a month" }),
  ).toHaveTextContent("Oct");

  await user.click(screen.getByRole("button", { name: "Chinese" }));
  await user.click(screen.getByRole("textbox"));
  expect(weekdays()).toEqual(["一", "二", "三", "四", "五", "六", "日"]);
});
