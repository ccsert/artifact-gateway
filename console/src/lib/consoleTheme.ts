// Public theme-system seam. Keep consumers on this module so theme package
// data, semantic resolution, Ant Design configuration, and DOM application
// cannot drift into parallel implementations.
export { defaultConsoleThemes } from "./theme/defaultConsoleThemes";
export {
  applyConsoleTheme,
  applyResolvedConsoleTheme,
  buildConsoleThemeConfig,
  resolveConsoleTheme,
  type ConsoleStatusRoles,
  type ConsoleThemeRoles,
  type ResolvedConsoleTheme,
} from "./theme/semanticTheme";
