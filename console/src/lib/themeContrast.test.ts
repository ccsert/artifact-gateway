import { describe, expect, it } from "vitest";
import { defaultConsoleThemes, resolveConsoleTheme } from "./consoleTheme";

type Rgba = [number, number, number, number];

function parse(color: string): Rgba {
  const hex = /^#([\da-f]{3}|[\da-f]{6})$/i.exec(color);
  if (hex) {
    const digits =
      hex[1].length === 3 ? [...hex[1]].map((d) => d + d).join("") : hex[1];
    return [0, 2, 4]
      .map((offset) => Number.parseInt(digits.slice(offset, offset + 2), 16))
      .concat(1) as Rgba;
  }
  const rgba = /^rgba?\(([^)]+)\)$/i.exec(color);
  if (!rgba) throw new Error(`unsupported colour ${color}`);
  const [r, g, b, a = "1"] = rgba[1].split(",").map((part) => part.trim());
  return [Number(r), Number(g), Number(b), Number(a)];
}

/** A translucent colour as it shows over an opaque one. */
function over(top: Rgba, bottom: Rgba): Rgba {
  const a = top[3];
  return [0, 1, 2]
    .map((i) => top[i] * a + bottom[i] * (1 - a))
    .concat(1) as Rgba;
}

function luminance([r, g, b]: Rgba) {
  const [lr, lg, lb] = [r, g, b].map((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * lr + 0.7152 * lg + 0.0722 * lb;
}

function contrast(foreground: Rgba, background: Rgba) {
  const [high, low] = [luminance(foreground), luminance(background)].sort(
    (a, b) => b - a,
  );
  return (high + 0.05) / (low + 0.05);
}

// WCAG AA for body text (#319) in the default Gateway themes. Extension
// packages own their palettes; disabled text is exempt by definition.
const gatewayThemes = defaultConsoleThemes.filter((theme) =>
  theme.id.startsWith("gateway-"),
);

it("covers both Gateway themes", () => {
  expect(gatewayThemes.map((theme) => theme.id).sort()).toEqual([
    "gateway-dark",
    "gateway-light",
  ]);
});

describe.each(gatewayThemes.map((theme) => [theme.id, theme] as const))(
  "%s contrast",
  (_id, theme) => {
    const { roles } = resolveConsoleTheme(theme);
    const canvas = parse(roles.surface.canvas);
    const surfaces = {
      canvas,
      container: over(parse(roles.surface.container), canvas),
    };
    const text = {
      "content.strong": roles.content.strong,
      "content.primary": roles.content.primary,
      "content.secondary": roles.content.secondary,
      "content.tertiary": roles.content.tertiary,
      "status.success": roles.status.success.foreground,
      "status.warning": roles.status.warning.foreground,
      "status.danger": roles.status.danger.foreground,
      "status.info": roles.status.info.foreground,
    };

    it.each(Object.entries(text))(
      "%s reads at 4.5:1 or more",
      (_role, color) => {
        for (const [name, surface] of Object.entries(surfaces)) {
          const ratio = contrast(over(parse(color), surface), surface);
          expect(ratio, `on ${name}`).toBeGreaterThanOrEqual(4.5);
        }
      },
    );
  },
);
