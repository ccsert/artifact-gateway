import { lazy, Suspense, type ComponentType } from "react";
import type { Format } from "../client";
import { Loading } from "../components/ui/Feedback";
import { formatDescriptor } from "./index";
import type {
  ArtifactDetailProps,
  FormatViews,
  PublishGuideProps,
} from "./viewTypes";

export type {
  ArtifactDetailProps,
  DetailArtifact,
  FormatViews,
  PublishGuideProps,
} from "./viewTypes";

/**
 * Each format's components load on first use, in that format's own chunk, so
 * a page showing npm packages never downloads the OCI or Maven views.
 */
const LOADERS: Record<Format, () => Promise<FormatViews>> = {
  oci: () => import("./oci/views").then((module) => module.ociViews),
  maven: () => import("./maven/views").then((module) => module.mavenViews),
  npm: () => import("./npm/views").then((module) => module.npmViews),
  pypi: () => import("./pypi/views").then((module) => module.pypiViews),
  go: () => import("./go/views").then((module) => module.goViews),
  cargo: () => import("./cargo/views").then((module) => module.cargoViews),
  conan: () => import("./conan/views").then((module) => module.conanViews),
  raw: () => import("./raw/views").then((module) => module.rawViews),
  apt: () => import("./apt/views").then((module) => module.aptViews),
};

const Nothing = () => null;

/**
 * A slot component that loads its format's views and suspends only itself,
 * so expanding one row never replaces the surrounding page with a fallback.
 */
function deferredSlot<P extends object>(
  format: Format,
  slot: keyof FormatViews,
): ComponentType<P> {
  const Loaded = lazy(async () => ({
    default: ((await LOADERS[format]())[slot] ?? Nothing) as ComponentType<P>,
  }));
  function DeferredSlot(props: P) {
    return (
      <Suspense fallback={<Loading />}>
        <Loaded {...props} />
      </Suspense>
    );
  }
  DeferredSlot.displayName = `${format}.${slot}`;
  return DeferredSlot;
}

// Slot components are created once per format, so their identity is stable
// across renders and a re-render never remounts an open detail.
const cache = new Map<Format, FormatViews>();

export function formatViews(format: string | undefined): FormatViews {
  const descriptor = formatDescriptor(format);
  if (!descriptor) return {};
  let views = cache.get(descriptor.format);
  if (!views) {
    views = {
      ArtifactDetail: deferredSlot<ArtifactDetailProps>(
        descriptor.format,
        "ArtifactDetail",
      ),
      PublishGuide: descriptor.publishGuide
        ? deferredSlot<PublishGuideProps>(descriptor.format, "PublishGuide")
        : undefined,
    };
    cache.set(descriptor.format, views);
  }
  return views;
}
