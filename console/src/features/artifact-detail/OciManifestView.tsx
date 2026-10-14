import type { ColumnsType } from "antd/es/table";
import { Badge } from "../../components/ui/Badge";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import type { OciConfig, OciDescriptor, OciManifest } from "./ociImageModel";

/** A manifest's size, platform, runtime configuration and layers. */
export function OciManifestView({
  manifest,
  config,
  manifestSize,
}: {
  manifest: OciManifest;
  config: OciConfig | null;
  manifestSize?: number;
}) {
  const { locale, t } = usePreferences();
  const totalSize =
    (manifest.layers ?? []).reduce((n, l) => n + (l.size ?? 0), 0) +
    (manifest.config?.size ?? 0);
  const hasImageDescriptors =
    manifest.config !== undefined || manifest.layers !== undefined;

  return (
    <>
      {/* 概要 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("artifactDetail.imageSize")}
          </div>
          <div className="mt-0.5 text-sm font-semibold text-fg-strong">
            {hasImageDescriptors
              ? formatBytes(totalSize)
              : t("artifactDetail.noLayerData")}
          </div>
          {!hasImageDescriptors && (
            <div className="mt-1 text-xs leading-4 text-fg-disabled">
              {t("artifactDetail.manifestMetadataOnly")}
            </div>
          )}
          <div className="mt-1 text-xs leading-4 text-fg-disabled">
            Manifest JSON {formatBytes(manifestSize)}
          </div>
        </div>
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("artifactDetail.layers")}
          </div>
          <div className="mt-0.5 text-sm font-semibold text-fg-strong">
            {manifest.layers?.length ?? 0}
          </div>
        </div>
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("artifactDetail.architectureOS")}
          </div>
          <div className="mt-0.5 text-sm font-semibold text-fg-strong">
            {config?.architecture ?? "—"} / {config?.os ?? "—"}
          </div>
        </div>
        <div className="rounded-lg border border-line px-3 py-2">
          <div className="text-xs text-fg-tertiary">
            {t("artifactDetail.created")}
          </div>
          <div className="mt-0.5 text-sm font-semibold text-fg-strong">
            {config?.created ? formatDate(config.created, locale) : "—"}
          </div>
        </div>
      </div>

      {/* 启动配置 */}
      {config?.config && (
        <div className="rounded-lg border border-line px-3 py-2.5 text-xs">
          <div className="mb-1.5 text-xs text-fg-tertiary">
            {t("artifactDetail.runtimeConfiguration")}
          </div>
          <div className="space-y-1 font-mono">
            {config.config.Entrypoint && (
              <div className="flex gap-2">
                <span className="w-20 shrink-0 text-fg-disabled">
                  Entrypoint
                </span>
                <span className="text-fg-secondary">
                  {config.config.Entrypoint.join(" ")}
                </span>
              </div>
            )}
            {config.config.Cmd && (
              <div className="flex gap-2">
                <span className="w-20 shrink-0 text-fg-disabled">Cmd</span>
                <span className="text-fg-secondary">
                  {config.config.Cmd.join(" ")}
                </span>
              </div>
            )}
            {config.config.User && (
              <div className="flex gap-2">
                <span className="w-20 shrink-0 text-fg-disabled">User</span>
                <span className="text-fg-secondary">{config.config.User}</span>
              </div>
            )}
          </div>
        </div>
      )}

      {/* 层列表 */}
      <div>
        <div className="mb-1.5 flex items-center gap-2 text-xs text-fg-tertiary">
          {t("artifactDetail.layers2")} ({manifest.layers?.length ?? 0})
          {manifest.mediaType && (
            <Badge tone="neutral">{manifest.mediaType.split(".").pop()}</Badge>
          )}
        </div>
        <ConsoleTable<OciDescriptor>
          rowKey="digest"
          size="small"
          pagination={false}
          scroll={{ x: 620 }}
          dataSource={manifest.layers ?? []}
          columns={
            [
              {
                title: "#",
                key: "index",
                width: 64,
                render: (_, __, index) => (
                  <span className="font-mono text-xs text-fg-tertiary">
                    #{index + 1}
                  </span>
                ),
              },
              {
                title: "Digest",
                dataIndex: "digest",
                key: "digest",
                ellipsis: true,
                render: (value: string) => (
                  <span
                    className="font-mono text-xs text-fg-secondary"
                    title={value}
                  >
                    {shortDigest(value)}
                  </span>
                ),
              },
              {
                title: t("artifactDetail.type"),
                dataIndex: "mediaType",
                key: "mediaType",
                width: 160,
                render: (value: string) => {
                  const kind = value.includes("gzip")
                    ? "tar+gzip"
                    : value.includes("tar")
                      ? "tar"
                      : (value.split(".").pop() ?? value);
                  return (
                    <span className="text-xs text-fg-secondary">{kind}</span>
                  );
                },
              },
              {
                title: t("artifactDetail.size"),
                dataIndex: "size",
                key: "size",
                width: 120,
                align: "right",
                render: (value: number) => (
                  <span className="text-xs text-fg-secondary">
                    {formatBytes(value)}
                  </span>
                ),
              },
            ] satisfies ColumnsType<OciDescriptor>
          }
        />
      </div>
    </>
  );
}
