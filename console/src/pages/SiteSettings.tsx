import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type CSSProperties,
} from "react";
import {
  DeleteOutlined,
  PictureOutlined,
  SaveOutlined,
  UndoOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { App, Button, Checkbox, Input, Radio, Upload } from "antd";
import {
  getSiteSettings,
  replaceSiteSettings,
  type ConsoleTheme,
  type SiteSettings,
} from "../client";
import { Card, PageHeader } from "../components/Layout";
import { ErrorBanner, Loading } from "../components/Feedback";
import { SiteBrandMark } from "../components/SiteBrand";
import { defaultSiteSettings, useSiteSettings } from "../lib/siteSettings";
import { usePreferences } from "../lib/preferences";
import { resolveConsoleTheme } from "../lib/consoleTheme";

const maxLogoBytes = 192 * 1024;
const supportedLogoTypes = new Set(["image/png", "image/jpeg", "image/webp"]);

interface SiteSettingsDraft {
  siteName: string;
  logoUrl: string;
  brandMark: string;
  enabledThemeIds: string[];
  defaultThemeId: string;
}

function toDraft(settings: SiteSettings): SiteSettingsDraft {
  return {
    siteName: settings.siteName,
    logoUrl: settings.logoUrl,
    brandMark: settings.brandMark,
    enabledThemeIds: [...settings.enabledThemeIds],
    defaultThemeId: settings.defaultThemeId,
  };
}

function ThemePreview({ theme }: { theme: ConsoleTheme }) {
  const { roles } = resolveConsoleTheme(theme);
  const style = {
    "--preview-bg": roles.surface.canvas,
    "--preview-surface": roles.surface.container,
    "--preview-border": roles.border.subtle,
    "--preview-text": roles.content.primary,
    "--preview-muted": roles.content.secondary,
    "--preview-primary": roles.action.primary,
  } as CSSProperties;
  return (
    <div className="ag-theme-preview" style={style} aria-hidden="true">
      <span className="ag-theme-preview-rail" />
      <span className="ag-theme-preview-content">
        <span className="ag-theme-preview-line ag-theme-preview-line-strong" />
        <span className="ag-theme-preview-line" />
        <span className="ag-theme-preview-action" />
      </span>
    </div>
  );
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.readAsDataURL(file);
  });
}

export function SiteSettingsPage() {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const { applySettings } = useSiteSettings();
  const [settings, setSettings] = useState<SiteSettings | null>(null);
  const [draft, setDraft] = useState<SiteSettingsDraft>(() =>
    toDraft(defaultSiteSettings),
  );
  const [initialError, setInitialError] = useState<unknown>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setInitialError(null);
    try {
      const { data, error } = await getSiteSettings();
      if (error || !data) {
        setInitialError(error ?? new Error("site settings are unavailable"));
        return;
      }
      setSettings(data);
      setDraft(toDraft(data));
      applySettings(data);
    } catch (error) {
      setInitialError(error);
    } finally {
      setLoading(false);
    }
  }, [applySettings]);

  useEffect(() => {
    void load();
  }, [load]);

  const normalizedDraft = useMemo(
    () => ({
      siteName: draft.siteName.trim(),
      logoUrl: draft.logoUrl.trim(),
      brandMark: draft.brandMark.trim(),
      enabledThemeIds: [...draft.enabledThemeIds],
      defaultThemeId: draft.defaultThemeId,
    }),
    [draft],
  );
  const dirty = settings
    ? JSON.stringify(normalizedDraft) !== JSON.stringify(toDraft(settings))
    : false;
  const valid =
    normalizedDraft.siteName.length > 0 &&
    normalizedDraft.siteName.length <= 80 &&
    normalizedDraft.brandMark.length > 0 &&
    Array.from(normalizedDraft.brandMark).length <= 8 &&
    normalizedDraft.enabledThemeIds.length > 0 &&
    normalizedDraft.enabledThemeIds.includes(normalizedDraft.defaultThemeId);

  const save = async () => {
    if (!settings || !valid) return;
    setSaving(true);
    setMutationError(null);
    try {
      const { data, error } = await replaceSiteSettings({
        body: normalizedDraft,
        headers: { "If-Match": settings.version },
      });
      if (error || !data) {
        setMutationError(error ?? new Error("site settings were not saved"));
        return;
      }
      setSettings(data);
      setDraft(toDraft(data));
      applySettings(data);
      void message.success(text("站点设置已生效", "Site settings are live"));
    } catch (error) {
      setMutationError(error);
    } finally {
      setSaving(false);
    }
  };

  const selectLogo = async (file: File) => {
    if (!supportedLogoTypes.has(file.type)) {
      void message.error(
        text(
          "仅支持 PNG、JPEG 或 WebP 图片",
          "Only PNG, JPEG, or WebP images are supported",
        ),
      );
      return;
    }
    if (file.size > maxLogoBytes) {
      void message.error(
        text(
          "Logo 文件不能超过 192 KiB",
          "The logo must be no larger than 192 KiB",
        ),
      );
      return;
    }
    try {
      const logoUrl = await readAsDataURL(file);
      setDraft((current) => ({ ...current, logoUrl }));
    } catch {
      void message.error(text("读取 Logo 失败", "Could not read the logo"));
    }
  };

  const setThemeEnabled = (id: string, enabled: boolean) => {
    setDraft((current) => {
      const enabledThemeIds = enabled
        ? current.enabledThemeIds.includes(id)
          ? current.enabledThemeIds
          : [...current.enabledThemeIds, id]
        : current.enabledThemeIds.filter((themeID) => themeID !== id);
      if (enabledThemeIds.length === 0) return current;
      return {
        ...current,
        enabledThemeIds,
        defaultThemeId: enabledThemeIds.includes(current.defaultThemeId)
          ? current.defaultThemeId
          : enabledThemeIds[0],
      };
    });
  };

  if (loading) return <Loading />;
  if (initialError !== null || !settings) {
    return (
      <div className="ag-page-stack">
        <PageHeader title={text("站点设置", "Site settings")} />
        <ErrorBanner error={initialError} onRetry={load} />
      </div>
    );
  }

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("站点设置", "Site settings")}
        description={text(
          "统一管理登录页、公开目录和控制台导航中的站点名称与品牌标识。",
          "Manage the site name and brand identity used by sign-in, public browse, and Console navigation.",
        )}
      />
      {mutationError !== null && <ErrorBanner error={mutationError} />}
      <Card bodyClassName="ag-site-settings-workspace">
        <form
          className="ag-site-settings-form"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <div className="ag-site-settings-section-heading">
            <div>
              <h2>{text("品牌信息", "Brand identity")}</h2>
              <p>
                {text(
                  "保存后将立即同步到当前会话，并供未登录页面公开读取。",
                  "Changes apply to this session immediately and are publicly readable before sign-in.",
                )}
              </p>
            </div>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-name">{text("站点名称", "Site name")}</label>
            <Input
              id="site-name"
              value={draft.siteName}
              maxLength={80}
              status={normalizedDraft.siteName ? undefined : "error"}
              placeholder="Artifact Gateway"
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  siteName: event.target.value,
                }))
              }
            />
            <small>
              {text(
                "显示在导航、登录页、公开目录和浏览器标题中。",
                "Shown in navigation, sign-in, public browse, and the browser title.",
              )}
            </small>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-brand-mark">
              {text("品牌标识", "Brand mark")}
            </label>
            <Input
              id="site-brand-mark"
              value={draft.brandMark}
              maxLength={8}
              status={normalizedDraft.brandMark ? undefined : "error"}
              placeholder="AG"
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  brandMark: event.target.value,
                }))
              }
            />
            <small>
              {text(
                "1–8 个字符；未设置 Logo 或 Logo 加载失败时作为回退。",
                "1–8 characters; used when no logo is set or the image cannot load.",
              )}
            </small>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-logo-url">
              {text("站点 Logo", "Site logo")}
            </label>
            <div className="ag-site-logo-input-row">
              <Input
                id="site-logo-url"
                value={draft.logoUrl}
                allowClear
                placeholder="/assets/logo.webp 或 https://…"
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    logoUrl: event.target.value,
                  }))
                }
              />
              <Upload
                accept="image/png,image/jpeg,image/webp"
                maxCount={1}
                showUploadList={false}
                beforeUpload={(file) => {
                  void selectLogo(file);
                  return false;
                }}
              >
                <Button
                  icon={<UploadOutlined />}
                  aria-label={text("上传 Logo", "Upload logo")}
                >
                  {text("上传", "Upload")}
                </Button>
              </Upload>
              {draft.logoUrl && (
                <Button
                  icon={<DeleteOutlined />}
                  aria-label={text("移除 Logo", "Remove logo")}
                  onClick={() =>
                    setDraft((current) => ({ ...current, logoUrl: "" }))
                  }
                />
              )}
            </div>
            <small>
              {text(
                "支持同源路径、HTTPS 地址，或上传不超过 192 KiB 的 PNG、JPEG、WebP。建议使用透明背景的方形图。",
                "Use a same-origin path, HTTPS URL, or upload a PNG, JPEG, or WebP up to 192 KiB. A square image with a transparent background works best.",
              )}
            </small>
          </div>

          <section className="ag-site-theme-settings">
            <div className="ag-site-settings-section-heading">
              <h2>{text("Console 主题", "Console themes")}</h2>
              <p>
                {text(
                  "启用用户可以选择的主题，并指定首次访问时使用的默认主题。主题颜色统一由 Ant Design token 解析。",
                  "Enable the themes users may choose and select the default for first-time visitors. All theme colors are resolved from Ant Design tokens.",
                )}
              </p>
            </div>
            <div className="ag-theme-option-grid">
              {settings.availableThemes.map((theme) => {
                const enabled = draft.enabledThemeIds.includes(theme.id);
                const onlyEnabled =
                  enabled && draft.enabledThemeIds.length === 1;
                return (
                  <article
                    key={theme.id}
                    className="ag-theme-option"
                    data-enabled={enabled}
                    data-theme-mode={theme.mode}
                  >
                    <div className="ag-theme-option-header">
                      <Checkbox
                        checked={enabled}
                        disabled={onlyEnabled}
                        onChange={(event) =>
                          setThemeEnabled(theme.id, event.target.checked)
                        }
                      >
                        <span className="ag-theme-option-name">
                          {theme.name}
                        </span>
                      </Checkbox>
                      <span className="ag-theme-option-mode">
                        {theme.mode === "dark"
                          ? text("深色", "Dark")
                          : text("浅色", "Light")}
                      </span>
                    </div>
                    <ThemePreview theme={theme} />
                    <p>{theme.description}</p>
                    <Radio
                      checked={draft.defaultThemeId === theme.id}
                      disabled={!enabled}
                      onChange={() =>
                        setDraft((current) => ({
                          ...current,
                          defaultThemeId: theme.id,
                        }))
                      }
                    >
                      {text("设为默认", "Use as default")}
                    </Radio>
                  </article>
                );
              })}
            </div>
            <p className="ag-theme-package-hint">
              {text(
                "新增主题无需修改 Console：让 AI 按 themes/console-theme.schema.json 生成配置，再运行 gateway theme validate 与 gateway theme install。",
                "Add themes without changing the Console: generate against themes/console-theme.schema.json, then run gateway theme validate and gateway theme install.",
              )}
            </p>
          </section>

          <div className="ag-site-settings-actions">
            <Button
              icon={<UndoOutlined />}
              aria-label={text("撤销更改", "Discard changes")}
              disabled={!dirty}
              onClick={() => setDraft(toDraft(settings))}
            >
              {text("撤销更改", "Discard changes")}
            </Button>
            <Button
              type="primary"
              htmlType="submit"
              icon={<SaveOutlined />}
              aria-label={text("保存并应用", "Save and apply")}
              loading={saving}
              disabled={!dirty || !valid}
            >
              {text("保存并应用", "Save and apply")}
            </Button>
          </div>
        </form>

        <aside
          className="ag-site-settings-preview"
          aria-label={text("品牌预览", "Brand preview")}
        >
          <div className="ag-site-preview-label">
            <PictureOutlined />
            {text("实时预览", "Live preview")}
          </div>
          <div className="ag-site-preview-lockup">
            <SiteBrandMark
              className="ag-site-preview-mark"
              preview={normalizedDraft}
            />
            <div className="min-w-0">
              <div className="ag-site-preview-name">
                {normalizedDraft.siteName ||
                  text("未命名站点", "Untitled site")}
              </div>
              <div className="ag-site-preview-product">Console</div>
            </div>
          </div>
          <div className="ag-site-preview-browser">
            <SiteBrandMark
              className="ag-site-preview-favicon"
              preview={normalizedDraft}
            />
            <span>
              {(normalizedDraft.siteName ||
                text("未命名站点", "Untitled site")) + " Console"}
            </span>
          </div>
          <p>
            {text(
              "预览仅展示身份组合；保存前不会影响其他用户。",
              "This preview shows the identity lockup only; other users are unaffected until you save.",
            )}
          </p>
        </aside>
      </Card>
    </div>
  );
}
