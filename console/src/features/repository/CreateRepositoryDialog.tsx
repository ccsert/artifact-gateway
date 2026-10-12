import { useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import { Button, Input, Segmented, Select, Space, Switch } from "antd";
import { createRepository } from "../../client";
import type { Format, FormatProfile } from "../../client";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { repositoryFormats, repositoryTypes } from "../../lib/formatProfiles";

/** Create a hosted or proxy repository of a format the Gateway serves. */
export function CreateRepositoryDialog({
  profiles,
  onCreated,
}: {
  profiles: FormatProfile[];
  onCreated: () => void;
}) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [format, setFormat] = useState<Format>("oci");
  const [type, setType] = useState<"hosted" | "proxy">("hosted");
  const [endpoint, setEndpoint] = useState("");
  const [allowedHosts, setAllowedHosts] = useState("");
  const [mavenStrictPublication, setMavenStrictPublication] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const formats = repositoryFormats(profiles, type);
  const selectedFormat = formats.includes(format)
    ? format
    : (formats[0] ?? format);
  const availableRepositoryTypes = repositoryTypes(profiles);

  const needsHosts =
    type === "proxy" &&
    (selectedFormat === "raw" ||
      selectedFormat === "conan" ||
      selectedFormat === "npm" ||
      selectedFormat === "pypi" ||
      selectedFormat === "go" ||
      selectedFormat === "cargo" ||
      selectedFormat === "apt");

  const submit = async () => {
    setBusy(true);
    setError(null);
    const hosts = allowedHosts
      .split(",")
      .map((h) => h.trim())
      .filter(Boolean);
    try {
      const { data, error: err } = await createRepository({
        body: {
          name: name.trim(),
          format: selectedFormat,
          type,
          ...(type === "proxy"
            ? { endpoint: endpoint.trim(), allowedHosts: hosts }
            : {}),
          ...(type === "hosted" && selectedFormat === "maven"
            ? { mavenStrictPublication }
            : {}),
        },
        headers: { "Idempotency-Key": crypto.randomUUID() },
      });
      if (err) {
        setError(err);
        return;
      }
      if (data) {
        dialog.hide();
        setName("");
        setEndpoint("");
        setAllowedHosts("");
        setMavenStrictPublication(false);
        setType("hosted");
        onCreated();
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const endpointPlaceholder: Record<string, string> = {
    oci: "https://registry-1.docker.io",
    maven: "https://repo1.maven.org/maven2",
    raw: "https://raw.githubusercontent.com",
    conan: "https://center.conan.io/v2",
    npm: "https://registry.npmjs.org",
    pypi: "https://pypi.org",
    go: "https://proxy.golang.org",
    cargo: "https://index.crates.io",
    apt: "https://deb.debian.org/debian",
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        disabled={profiles.length === 0}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {text("新建仓库", "New repository")}
      </Button>
      <Modal
        open={dialog.open}
        title={text("新建仓库", "New repository")}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={
                busy ||
                profiles.length === 0 ||
                !name.trim() ||
                (type === "proxy" &&
                  (!endpoint.trim() || (needsHosts && !allowedHosts.trim())))
              }
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label={text("类型", "Type")} group>
            <Segmented<"hosted" | "proxy">
              block
              value={type}
              onChange={(nextType) => {
                setType(nextType);
                const nextFormats = repositoryFormats(profiles, nextType);
                if (!nextFormats.includes(format) && nextFormats[0]) {
                  setFormat(nextFormats[0]);
                }
              }}
              options={availableRepositoryTypes.map((repositoryType) => ({
                value: repositoryType,
                label:
                  repositoryType === "hosted"
                    ? text("托管 (hosted)", "Hosted")
                    : text("代理 (proxy)", "Proxy"),
              }))}
            />
            <span className="mt-1 block text-xs text-fg-disabled">
              {type === "hosted"
                ? text("自己托管制品，可推送", "Host and publish artifacts")
                : text("从上游仓库拉取并缓存", "Fetch and cache from upstream")}
            </span>
          </Field>
          <Field
            label={text("仓库名称", "Repository name")}
            hint={text(
              "小写字母、数字与连字符，例如 team-images",
              "Lowercase letters, numbers, and hyphens, for example team-images",
            )}
          >
            <Input
              className="font-mono"
              placeholder="my-repository"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={text("格式", "Format")} group>
            <Select<Format>
              className="w-full font-mono"
              showSearch={{ optionFilterProp: "label" }}
              value={selectedFormat}
              onChange={setFormat}
              options={formats.map((candidate) => ({
                value: candidate,
                label: candidate,
              }))}
            />
          </Field>
          {type === "hosted" && selectedFormat === "maven" && (
            <div className="flex flex-wrap items-start justify-between gap-4 rounded-lg border border-line bg-surface-translucent px-4 py-3">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-fg">
                  {text("严格发布", "Strict publication")}
                </div>
                <div className="mt-1 max-w-[62ch] text-xs leading-5 text-fg-tertiary">
                  {text(
                    "默认关闭：Maven/Gradle 标准上传成功后立即可读，便于从 Nexus 直接迁移。开启后，同一坐标会在 Gateway commit 成功前保持不可见。",
                    "Off by default: successful standard Maven/Gradle uploads are immediately readable for Nexus-compatible migration. When enabled, a coordinate stays hidden until its Gateway commit succeeds.",
                  )}
                </div>
              </div>
              <Switch
                checked={mavenStrictPublication}
                onChange={setMavenStrictPublication}
                aria-label={text("严格发布", "Strict publication")}
              />
            </div>
          )}
          {type === "proxy" && (
            <>
              <Field
                label={text("上游地址 endpoint", "Upstream endpoint")}
                hint={text("代理拉取的外部仓库地址", "External proxy source")}
              >
                <Input
                  className="font-mono text-xs"
                  placeholder={
                    endpointPlaceholder[selectedFormat] ??
                    "https://registry.example.com"
                  }
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label={text(
                  `允许主机 allowedHosts${needsHosts ? "（必填）" : "（可选）"}`,
                  `Allowed hosts${needsHosts ? " (required)" : " (optional)"}`,
                )}
                hint={text(
                  "逗号分隔的主机名；Raw、Conan、npm、PyPI、Go、Cargo 和 APT 代理必填。PyPI 通常还需允许 files.pythonhosted.org；Cargo 通常还需 static.crates.io。",
                  "Comma-separated hostnames; required for Raw, Conan, npm, PyPI, Go, Cargo, and APT proxies. PyPI normally also requires files.pythonhosted.org; Cargo normally also requires static.crates.io.",
                )}
              >
                <Input
                  className="font-mono text-xs"
                  placeholder={
                    selectedFormat === "npm"
                      ? "registry.npmjs.org"
                      : selectedFormat === "pypi"
                        ? "files.pythonhosted.org"
                        : selectedFormat === "go"
                          ? "proxy.golang.org"
                          : selectedFormat === "cargo"
                            ? "index.crates.io, static.crates.io"
                            : selectedFormat === "apt"
                              ? "deb.debian.org"
                              : "repo1.maven.org"
                  }
                  value={allowedHosts}
                  onChange={(e) => setAllowedHosts(e.target.value)}
                />
              </Field>
            </>
          )}
        </div>
      </Modal>
    </>
  );
}
