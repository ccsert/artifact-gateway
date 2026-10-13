import { useState } from "react";
import { DownOutlined, KeyOutlined, LogoutOutlined } from "@ant-design/icons";
import { Button, Dropdown, Input, Space } from "antd";
import { useAuth } from "../lib/auth";
import { Modal, useDisclosure } from "../components/ui/Modal";
import { Field } from "../components/ui/Layout";
import { usePreferences } from "../lib/preferences";
import { type CurrentIdentity } from "../client";

function TokenDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { token, setToken, clearToken } = useAuth();
  const { t } = usePreferences();
  // Mounted once per opening, so each opening starts from the saved token.
  const [draft, setDraft] = useState(token);

  return (
    <Modal
      open={open}
      title={t("auth.tokenDialog")}
      onClose={onClose}
      footer={
        <div className="flex items-center justify-between gap-4">
          <div>
            {token && (
              <Button
                danger
                onClick={() => {
                  clearToken();
                  onClose();
                }}
              >
                {t("auth.clearToken")}
              </Button>
            )}
          </div>
          <Space>
            <Button onClick={onClose}>{t("common.cancel")}</Button>
            <Button
              type="primary"
              disabled={!draft.trim()}
              onClick={() => {
                setToken(draft);
                window.location.reload();
              }}
            >
              {t("auth.saveToken")}
            </Button>
          </Space>
        </div>
      }
    >
      <Field label="Bearer Token" hint={t("auth.tokenDialogHint")}>
        <Input.TextArea
          className="font-mono text-xs"
          autoSize={{ minRows: 4, maxRows: 8 }}
          placeholder={t("auth.tokenPlaceholder")}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
        />
      </Field>
    </Modal>
  );
}

/**
 * Who is signed in, and how. Token management and sign-out live here instead
 * of the old top bar, so the button states the real authentication method.
 */
export function AccountMenu({
  identity,
  collapsed = false,
  onSignOut,
}: {
  identity: CurrentIdentity | null;
  collapsed?: boolean;
  onSignOut: () => void;
}) {
  const { token } = useAuth();
  const { t } = usePreferences();
  const tokenDialog = useDisclosure();
  const [tokenDialogOpening, setTokenDialogOpening] = useState(0);
  const actor = identity?.actor ?? "—";
  const initial =
    actor
      .replace(/^[a-z-]+:/, "")
      .charAt(0)
      .toUpperCase() || "?";
  const kind = identity ? t(`account.kind.${identity.kind}`) : "";
  const role =
    identity?.role && identity.role !== "none"
      ? t(`account.role.${identity.role}`)
      : "";
  const summary = [role, kind].filter(Boolean).join(" · ");

  return (
    <>
      <Dropdown
        trigger={["click"]}
        placement="topLeft"
        menu={{
          items: [
            {
              key: "token",
              icon: <KeyOutlined />,
              label: token ? t("auth.tokenConfigured") : t("auth.setToken"),
              onClick: () => {
                setTokenDialogOpening((count) => count + 1);
                tokenDialog.show();
              },
            },
            { type: "divider" },
            {
              key: "sign-out",
              icon: <LogoutOutlined />,
              label: t("auth.logout"),
              onClick: onSignOut,
            },
          ],
        }}
      >
        <button
          type="button"
          className="ag-account-button"
          data-collapsed={collapsed ? "true" : "false"}
          aria-label={`${t("account.menu")}: ${actor}`}
        >
          <span className="ag-account-avatar" aria-hidden="true">
            {initial}
          </span>
          <span className="ag-account-copy">
            <span className="ag-account-name">{actor}</span>
            {summary && <span className="ag-account-meta">{summary}</span>}
          </span>
          <DownOutlined className="ag-account-caret" aria-hidden="true" />
        </button>
      </Dropdown>
      <TokenDialog
        key={tokenDialogOpening}
        open={tokenDialog.open}
        onClose={tokenDialog.hide}
      />
    </>
  );
}
