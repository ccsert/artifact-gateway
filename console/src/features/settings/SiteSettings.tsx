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
import "./settings.css";

export function SiteSettingsPage() {
  const { message } = App.useApp();
  const { t } = usePreferences();
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
      void message.success(t("settings.siteSettingsAreLive"));
    } catch (error) {
      setMutationError(error);
    } finally {
      setSaving(false);
    }
  };

  const selectLogo = async (file: File) => {
    if (!supportedLogoTypes.has(file.type)) {
      void message.error(t("settings.onlyPNGJPEGOrWebPImagesAreSupported"));
      return;
    }
    if (file.size > maxLogoBytes) {
      void message.error(t("settings.theLogoMustBeNoLargerThan192"));
      return;
    }
    try {
      const logoUrl = await readAsDataURL(file);
      setDraft((current) => ({ ...current, logoUrl }));
    } catch {
      void message.error(t("settings.couldNotReadTheLogo"));
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
  if (!settings) {
    return (
      <div className="ag-page-stack">
        <PageHeader title={t("settings.siteSettings")} />
        <ErrorBanner error={initialError} onRetry={load} />
      </div>
    );
  }

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("settings.siteSettings")}
        description={t("settings.manageTheSiteNameAndBrandIdentityUsed")}
      />
      {initialError !== null && (
        <ErrorBanner error={initialError} onRetry={load} />
      )}
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
              <h2>{t("settings.brandIdentity")}</h2>
              <p>{t("settings.changesApplyToThisSessionImmediatelyAndAre")}</p>
            </div>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-name">{t("settings.siteName")}</label>
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
              {t("settings.shownInNavigationSignInPublicBrowseAnd")}
            </small>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-brand-mark">{t("settings.brandMark")}</label>
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
            <small>{t("settings.18CharactersUsedWhenNoLogoIs")}</small>
          </div>

          <div className="ag-site-settings-field">
            <label htmlFor="site-logo-url">{t("settings.siteLogo")}</label>
            <div className="ag-site-logo-input-row">
              <Input
                id="site-logo-url"
                value={draft.logoUrl}
                allowClear
                placeholder={t("settings.logoUrlPlaceholder")}
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
                  aria-label={t("settings.uploadLogo")}
                >
                  {t("settings.upload")}
                </Button>
              </Upload>
              {draft.logoUrl && (
                <Button
                  icon={<DeleteOutlined />}
                  aria-label={t("settings.removeLogo")}
                  onClick={() =>
                    setDraft((current) => ({ ...current, logoUrl: "" }))
                  }
                />
              )}
            </div>
            <small>{t("settings.useASameOriginPathHTTPSURLOr")}</small>
          </div>

          <section className="ag-site-theme-settings">
            <div className="ag-site-settings-section-heading ag-site-settings-section-heading-action">
              <div>
                <h2>{t("settings.consoleThemes")}</h2>
                <p>{t("settings.enableTheThemesUsersMayChooseAndSelect")}</p>
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
                  aria-label={t("settings.uploadThemePackage")}
                >
                  {t("settings.uploadThemePackage")}
                </Button>
              </Upload>
            </div>
            {packages.themePackageError !== null &&
              packages.pendingThemePackage === null && (
                <ErrorBanner
                  error={packages.themePackageError}
                  title={t("settings.themePackageFailed")}
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
              {t("settings.uploadAThemePackageV1MatchingThemesConsole")}
            </p>
          </section>

          <div className="ag-site-settings-actions">
            <Button
              icon={<UndoOutlined />}
              aria-label={t("settings.discardChanges")}
              disabled={!dirty}
              onClick={() => setEdited(null)}
            >
              {t("settings.discardChanges")}
            </Button>
            <Button
              type="primary"
              htmlType="submit"
              icon={<SaveOutlined />}
              aria-label={t("settings.saveAndApply")}
              loading={saving}
              disabled={!dirty || !valid}
            >
              {t("settings.saveAndApply")}
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
