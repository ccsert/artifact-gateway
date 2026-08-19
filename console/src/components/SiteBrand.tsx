import { useState, type CSSProperties } from "react";
import type { SiteSettings } from "../client";
import { useSiteSettings } from "../lib/siteSettings";

function SiteBrandImage({ src }: { src: string }) {
  const [failed, setFailed] = useState(false);
  if (failed) return null;
  return (
    <img
      className="ag-brand-mark-image"
      src={src}
      alt=""
      onError={() => setFailed(true)}
    />
  );
}

export function SiteBrandMark({
  className = "",
  preview,
}: {
  className?: string;
  preview?: Pick<SiteSettings, "logoUrl" | "brandMark">;
}) {
  const { settings: current } = useSiteSettings();
  const settings = preview ?? current;
  const markLength = Math.max(1, Array.from(settings.brandMark).length);
  const markStyle = {
    "--ag-brand-mark-font-size": `${0.78 * Math.min(1, 2 / markLength)}em`,
  } as CSSProperties;

  return (
    <span
      className={`ag-brand-mark ${className}`}
      data-has-logo={settings.logoUrl ? "true" : "false"}
      style={markStyle}
      aria-hidden="true"
    >
      {settings.logoUrl && (
        <SiteBrandImage key={settings.logoUrl} src={settings.logoUrl} />
      )}
      <span className="ag-brand-mark-text">{settings.brandMark}</span>
    </span>
  );
}

export function SiteName() {
  const { settings } = useSiteSettings();
  return <>{settings.siteName}</>;
}
