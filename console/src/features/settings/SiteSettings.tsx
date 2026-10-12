import { useMemo, useState } from "react";
import {
  DeleteOutlined,
  SaveOutlined,
  UndoOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { App, Button, Input, Upload } from "antd";
import { replaceSiteSettings } from "../../client";
import { Card, PageHeader } from "../../components/ui/Layout";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  defaultSiteSettings,
  useSiteSettings,
  useSiteSettingsQuery,
} from "../../lib/siteSettings";
import { usePreferences } from "../../lib/preferences";
import { SiteBrandPreview } from "./SiteBrandPreview";
import { SiteThemeOption } from "./SiteThemeOption";
import {
  maxEnabledThemes,
  maxLogoBytes,
  readAsDataURL,
  supportedLogoTypes,
  toDraft,
  type SiteSettingsDraft,
} from "./siteSettingsModel";
import { ThemePackageReviewDialog } from "./ThemePackageReviewDialog";
import { useThemePackages } from "./useThemePackages";

export function SiteSettingsPage() {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const { applySettings } = useSiteSettings();
  const settingsQuery = useSiteSettingsQuery({ fresh: true });
  const settings = settingsQuery.data ?? null;
  const initialError = settingsQuery.error;
  // The form edits a draft over the saved settings; without edits it follows
  // them, including themes installed or deleted on this page.
  const [edited, setEdited] = useState<SiteSettingsDraft | null>(null);
  const draft = edited ?? toDraft(settings ?? defaultSiteSettings);
  const setDraft = (
    update: (current: SiteSettingsDraft) => SiteSettingsDraft,
  ) => setEdited((current) => update(current ?? draft));
  const setSettings = applySettings;
  const [mutationError, setMutationError] = useState<unknown>(null);
  const loading = settingsQuery.isPending;
  const [saving, setSaving] = useState(false);

  const load = () => settingsQuery.refetch();
  const packages = useThemePackages({
    settings,
    draft,
    setSettings,
    setDraft,
  });

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
    normalizedDraft.enabledThemeIds.length <= maxEnabledThemes &&
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
      setEdited(null);
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
            <div className="ag-site-settings-section-heading ag-site-settings-section-heading-action">
              <div>
                <h2>{text("Console 主题", "Console themes")}</h2>
                <p>
                  {text(
                    "启用用户可以选择的主题，并指定首次访问时使用的默认主题。主题颜色统一由 Ant Design token 解析。",
                    "Enable the themes users may choose and select the default for first-time visitors. All theme colors are resolved from Ant Design tokens.",
                  )}
                </p>
              </div>
              <Upload
                accept=".json,application/json"
                maxCount={1}
                showUploadList={false}
                beforeUpload={(file) => {
                  void packages.selectThemePackage(file);
                  return false;
                }}
              >
                <Button
                  icon={<UploadOutlined />}
                  loading={packages.validatingThemePackage}
                  aria-label={text("上传主题包", "Upload theme package")}
                >
                  {text("上传主题包", "Upload theme package")}
                </Button>
              </Upload>
            </div>
            {packages.themePackageError !== null &&
              packages.pendingThemePackage === null && (
                <ErrorBanner
                  error={packages.themePackageError}
                  title={text("主题包处理失败", "Theme package failed")}
                />
              )}
            <div className="ag-theme-option-grid">
              {settings.availableThemes.map((theme) => {
                const enabled = draft.enabledThemeIds.includes(theme.id);
                const onlyEnabled =
                  enabled && draft.enabledThemeIds.length === 1;
                const persistedEnabled = settings.enabledThemeIds.includes(
                  theme.id,
                );
                const deletionBlocked = persistedEnabled || enabled;
                return (
                  <SiteThemeOption
                    key={theme.id}
                    theme={theme}
                    enabled={enabled}
                    onlyEnabled={onlyEnabled}
                    deletionBlocked={deletionBlocked}
                    isDefault={draft.defaultThemeId === theme.id}
                    deleting={packages.deletingThemeID === theme.id}
                    onToggle={(checked) => setThemeEnabled(theme.id, checked)}
                    onMakeDefault={() =>
                      setDraft((current) => ({
                        ...current,
                        defaultThemeId: theme.id,
                      }))
                    }
                    onDelete={() => void packages.deleteManagedTheme(theme)}
                  />
                );
              })}
            </div>
            <p className="ag-theme-package-hint">
              {text(
                "上传符合 themes/console-theme.schema.json 的 Theme Package v1 即可动态安装；主题包只允许受限颜色 token，不会执行 CSS、脚本或外部资源。运维目录主题仍可通过 gateway theme 命令管理。",
                "Upload a Theme Package v1 matching themes/console-theme.schema.json to install it dynamically. Packages contain bounded color tokens only—no CSS, scripts, or external assets. Operator directory themes remain available through gateway theme commands.",
              )}
            </p>
          </section>

          <div className="ag-site-settings-actions">
            <Button
              icon={<UndoOutlined />}
              aria-label={text("撤销更改", "Discard changes")}
              disabled={!dirty}
              onClick={() => setEdited(null)}
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

        <SiteBrandPreview normalizedDraft={normalizedDraft} />
      </Card>
      <ThemePackageReviewDialog
        pending={packages.pendingThemePackage}
        installing={packages.installingThemePackage}
        error={packages.themePackageError}
        onClose={packages.closeThemePackage}
        onCommit={() => void packages.commitThemePackage()}
      />
    </div>
  );
}
