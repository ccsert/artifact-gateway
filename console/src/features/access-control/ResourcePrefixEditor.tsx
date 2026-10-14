import { AutoComplete, Input, Segmented, Select } from "antd";
import type { Repository } from "../../client";
import { usePreferences } from "../../lib/preferences";

type Format = Repository["format"];

export const RESOURCE_PREFIX_EXAMPLES: Record<Format, string[]> = {
  maven: ["com.example", "com.example:gateway"],
  oci: ["team/", "team/backend"],
  conan: ["pkg/", "pkg/1.0/", "pkg/1.0/team/stable"],
  raw: ["releases/", "snapshots/"],
  npm: ["@scope/", "@scope/package"],
  pypi: ["gateway-", "internal-"],
  go: ["example.com/team/", "git.example.com/platform/"],
  cargo: ["team-", "demo-crate"],
  apt: ["dists/bookworm/", "pool/main/"],
};

export function buildResourcePrefix(format: Format, parts: string[]): string {
  const clean = parts.map((value) => value.trim());
  switch (format) {
    case "maven":
      return [clean[0], clean[1]].filter(Boolean).join(":");
    case "oci": {
      const namespace = (clean[0] ?? "").replace(/^\/+|\/+$/g, "");
      const image = (clean[1] ?? "").replace(/^\/+|\/+$/g, "");
      if (namespace && image) return `${namespace}/${image}`;
      if (namespace) return `${namespace}/`;
      return image;
    }
    case "conan": {
      const values = clean.slice(0, 4).filter(Boolean);
      if (values.length === 0) return "";
      return `${values.join("/")}${values.length < 4 ? "/" : ""}`;
    }
    case "npm": {
      const rawScope = clean[0] ?? "";
      const scope = rawScope
        ? `@${rawScope.replace(/^@/, "").replace(/\/$/, "")}`
        : "";
      const packageName = (clean[1] ?? "").replace(/^\/+/, "");
      if (scope && packageName) return `${scope}/${packageName}`;
      if (scope) return `${scope}/`;
      return packageName;
    }
    default:
      return clean[0] ?? "";
  }
}

export function parseResourcePrefix(format: Format, value: string): string[] {
  switch (format) {
    case "maven":
      return value.split(":", 2);
    case "oci": {
      if (value.endsWith("/")) return [value.replace(/\/+$/, ""), ""];
      const trimmed = value.replace(/\/$/, "");
      const separator = trimmed.lastIndexOf("/");
      return separator < 0
        ? ["", trimmed]
        : [trimmed.slice(0, separator), trimmed.slice(separator + 1)];
    }
    case "conan":
      return value.replace(/\/$/, "").split("/", 4);
    case "npm": {
      if (!value.startsWith("@")) return ["", value];
      const separator = value.indexOf("/");
      return separator < 0
        ? [value, ""]
        : [value.slice(0, separator), value.slice(separator + 1)];
    }
    default:
      return [value];
  }
}

export function inferResourcePrefixFormat(
  value: string,
  formats: Format[],
): Format {
  const available = formats.length
    ? formats
    : (Object.keys(RESOURCE_PREFIX_EXAMPLES) as Format[]);
  const preferred = value.startsWith("@")
    ? "npm"
    : value.startsWith("dists/") || value.startsWith("pool/")
      ? "apt"
      : value.includes(":")
        ? "maven"
        : undefined;
  return preferred && available.includes(preferred) ? preferred : available[0];
}

type Props = {
  format: Format;
  value?: string;
  onChange: (value: string) => void;
  formats?: Format[];
  onFormatChange?: (format: Format) => void;
};

export function ResourcePrefixEditor({
  format,
  value = "",
  onChange,
  formats,
  onFormatChange,
}: Props) {
  const { t } = usePreferences();
  const parts = parseResourcePrefix(format, value);
  const updatePart = (index: number, next: string) => {
    const updated = [...parts];
    updated[index] = next;
    onChange(buildResourcePrefix(format, updated));
  };
  const options = (formats?.length ? formats : [format]).map((item) => ({
    value: item,
    label: item.toUpperCase(),
  }));

  const singleField = (
    placeholder: string,
    examples = RESOURCE_PREFIX_EXAMPLES[format],
  ) => (
    <AutoComplete
      className="w-full font-mono"
      allowClear
      maxLength={255}
      value={parts[0] ?? ""}
      options={examples.map((example) => ({ value: example }))}
      placeholder={placeholder}
      onChange={(next) => updatePart(0, next)}
    />
  );

  const fields = (() => {
    switch (format) {
      case "maven":
        return (
          <div className="grid grid-cols-2 gap-2">
            <Input
              className="font-mono"
              value={parts[0] ?? ""}
              placeholder="groupId · com.example"
              onChange={(event) => updatePart(0, event.target.value)}
            />
            <Input
              className="font-mono"
              value={parts[1] ?? ""}
              placeholder={t("access.artifactIdOptional")}
              onChange={(event) => updatePart(1, event.target.value)}
            />
          </div>
        );
      case "oci":
        return (
          <div className="grid grid-cols-2 gap-2">
            <Input
              className="font-mono"
              value={parts[0] ?? ""}
              placeholder={t("access.namespaceTeam")}
              onChange={(event) => updatePart(0, event.target.value)}
            />
            <Input
              className="font-mono"
              value={parts[1] ?? ""}
              placeholder={t("access.imageNameOptional")}
              onChange={(event) => updatePart(1, event.target.value)}
            />
          </div>
        );
      case "conan":
        return (
          <div className="grid grid-cols-2 gap-2">
            {[
              t("access.name"),
              t("access.versionOptional"),
              t("access.userOptional"),
              t("access.channelOptional"),
            ].map((placeholder, index) => (
              <Input
                key={placeholder}
                className="font-mono"
                value={parts[index] ?? ""}
                placeholder={placeholder}
                onChange={(event) => updatePart(index, event.target.value)}
              />
            ))}
          </div>
        );
      case "npm":
        return (
          <div className="grid grid-cols-2 gap-2">
            <Input
              className="font-mono"
              value={parts[0] ?? ""}
              placeholder={t("access.scopeTeam")}
              onChange={(event) => updatePart(0, event.target.value)}
            />
            <Input
              className="font-mono"
              value={parts[1] ?? ""}
              placeholder={t("access.packageOptional")}
              onChange={(event) => updatePart(1, event.target.value)}
            />
          </div>
        );
      case "raw":
        return singleField(t("access.pathPrefixReleases"));
      case "pypi":
        return singleField(t("access.projectPrefixGateway"));
      case "go":
        return singleField(t("access.modulePathPrefixExampleComTeam"));
      case "apt":
        return singleField(t("access.repositoryPathPrefixDistsBookworm"));
    }
  })();

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        {onFormatChange && (
          <Select<Format>
            className="w-28 shrink-0"
            value={format}
            options={options}
            onChange={(next) => {
              onFormatChange(next);
              onChange("");
            }}
          />
        )}
        <Segmented
          className="min-w-0 flex-1"
          block
          size="small"
          value={value ? "prefix" : "all"}
          options={[
            { value: "all", label: t("access.entireRepository") },
            { value: "prefix", label: t("access.limitByPrefix") },
          ]}
          onChange={(mode) =>
            onChange(mode === "all" ? "" : RESOURCE_PREFIX_EXAMPLES[format][0])
          }
        />
      </div>
      {value && (
        <>
          {fields}
          <div
            className="truncate font-mono text-xs text-fg-tertiary"
            title={value}
          >
            {t("access.canonicalPrefix")}: {value}
          </div>
        </>
      )}
    </div>
  );
}
