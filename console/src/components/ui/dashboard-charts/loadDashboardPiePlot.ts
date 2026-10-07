type PlotModule = typeof import("./DashboardPiePlot");

let loaded: PlotModule | undefined;
let pending: Promise<PlotModule> | undefined;

// Keep the module lazy, but overlap its download with the overview requests.
export function loadDashboardPiePlot() {
  pending ??= import("./DashboardPiePlot").then(
    (module) => {
      loaded = module;
      return module;
    },
    (cause: unknown) => {
      pending = undefined;
      throw cause;
    },
  );
  return pending;
}

export function getLoadedDashboardPiePlot() {
  return loaded?.default;
}

export function preloadDashboardPiePlot() {
  // A failed speculative download must not create an unhandled rejection.
  // The actual lazy render still owns the import result.
  void loadDashboardPiePlot().catch(() => undefined);
}
