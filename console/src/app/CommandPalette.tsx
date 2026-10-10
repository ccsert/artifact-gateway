import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import {
  BgColorsOutlined,
  GlobalOutlined,
  InboxOutlined,
  LogoutOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Input, Modal, type InputRef } from "antd";
import { useNavigate } from "react-router-dom";
import { listRepositories, type Repository } from "../client";
import { FormatBadge, RepositoryTypeBadge } from "../components/ui/Badge";
import { usePreferences } from "../lib/preferences";

export interface CommandPage {
  to: string;
  label: string;
  icon: ReactNode;
}

interface Command {
  id: string;
  group: "pages" | "repositories" | "actions";
  label: string;
  meta?: ReactNode;
  icon: ReactNode;
  run: () => void;
}

const MAX_REPOSITORIES = 8;

/** Opens on ⌘K / Ctrl+K anywhere in the authenticated shell. */
export function useCommandPaletteShortcut(open: () => void) {
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (
        (event.metaKey || event.ctrlKey) &&
        !event.altKey &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault();
        open();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);
}

export function CommandPalette({
  open,
  onClose,
  pages,
  canBrowseRepositories,
  onSignOut,
}: {
  open: boolean;
  onClose: () => void;
  pages: readonly CommandPage[];
  canBrowseRepositories: boolean;
  onSignOut: () => void;
}) {
  const { t, text, locale, setLocale, toggleColorMode } = usePreferences();
  const navigate = useNavigate();
  const inputRef = useRef<InputRef>(null);
  const listId = useId();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [repositories, setRepositories] = useState<Repository[] | null>(null);

  useEffect(() => {
    if (!open || !canBrowseRepositories || repositories) return;
    let cancelled = false;
    void listRepositories({ query: { pageSize: 100 } }).then(({ data }) => {
      if (!cancelled) setRepositories(data?.items ?? []);
    });
    return () => {
      cancelled = true;
    };
  }, [open, canBrowseRepositories, repositories]);

  const commands = useMemo<Command[]>(() => {
    const term = query.trim().toLocaleLowerCase();
    const matches = (...values: (string | undefined)[]) =>
      !term ||
      values.some((value) => value?.toLocaleLowerCase().includes(term));
    const go = (to: string) => () => navigate(to);

    const pageCommands: Command[] = pages
      .filter((page) => matches(page.label, page.to))
      .map((page) => ({
        id: `page:${page.to}`,
        group: "pages",
        label: page.label,
        meta: <span className="font-mono">{page.to}</span>,
        icon: page.icon,
        run: go(page.to),
      }));

    const repositoryCommands: Command[] = (repositories ?? [])
      .filter((repository) => repository.state !== "deleted")
      .filter((repository) =>
        matches(repository.name, repository.format, repository.type),
      )
      .slice(0, MAX_REPOSITORIES)
      .map((repository) => ({
        id: `repository:${repository.id}`,
        group: "repositories",
        label: repository.name,
        meta: (
          <>
            <FormatBadge format={repository.format} />
            <RepositoryTypeBadge type={repository.type} />
          </>
        ),
        icon: <InboxOutlined />,
        run: go(`/repositories/${encodeURIComponent(repository.id)}`),
      }));

    const actionCommands: Command[] = [];
    if (term) {
      actionCommands.push({
        id: "action:search",
        group: "actions",
        label: t("palette.searchArtifacts", { query: query.trim() }),
        icon: <SearchOutlined />,
        run: go(`/search?q=${encodeURIComponent(query.trim())}`),
      });
    }
    const toggleTheme: Command = {
      id: "action:theme",
      group: "actions",
      label: t("palette.toggleTheme"),
      icon: <BgColorsOutlined />,
      run: toggleColorMode,
    };
    const switchLanguage: Command = {
      id: "action:language",
      group: "actions",
      label: locale === "zh-CN" ? "Switch to English" : "切换到中文",
      icon: <GlobalOutlined />,
      run: () => setLocale(locale === "zh-CN" ? "en-US" : "zh-CN"),
    };
    const signOut: Command = {
      id: "action:sign-out",
      group: "actions",
      label: t("auth.logout"),
      icon: <LogoutOutlined />,
      run: onSignOut,
    };
    for (const command of [toggleTheme, switchLanguage, signOut]) {
      if (matches(command.label)) actionCommands.push(command);
    }

    return [...pageCommands, ...repositoryCommands, ...actionCommands];
  }, [
    query,
    pages,
    repositories,
    navigate,
    t,
    locale,
    setLocale,
    toggleColorMode,
    onSignOut,
  ]);

  useEffect(() => setActive(0), [query]);

  const close = () => {
    onClose();
    setQuery("");
  };

  const runCommand = (command: Command | undefined) => {
    if (!command) return;
    close();
    command.run();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((index) => Math.min(index + 1, commands.length - 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((index) => Math.max(index - 1, 0));
    } else if (event.key === "Enter") {
      event.preventDefault();
      runCommand(commands[active]);
    }
  };

  const groupLabels: Record<Command["group"], string> = {
    pages: t("palette.pages"),
    repositories: t("palette.repositories"),
    actions: t("palette.actions"),
  };
  const optionId = (index: number) => `${listId}-option-${index}`;

  return (
    <Modal
      open={open}
      onCancel={close}
      footer={null}
      closable={false}
      width={640}
      style={{ top: 96 }}
      rootClassName="ag-command-palette"
      destroyOnHidden
      afterOpenChange={(visible) => {
        if (visible) inputRef.current?.focus();
      }}
      title={<span className="sr-only">{t("palette.title")}</span>}
    >
      <Input
        ref={inputRef}
        className="ag-command-input"
        variant="borderless"
        size="large"
        prefix={<SearchOutlined aria-hidden="true" />}
        placeholder={t("palette.placeholder")}
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={onKeyDown}
        role="combobox"
        aria-expanded="true"
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={
          commands.length > 0 ? optionId(active) : undefined
        }
        aria-label={t("palette.title")}
      />
      <div
        id={listId}
        role="listbox"
        aria-label={t("palette.results")}
        className="ag-command-list"
      >
        {commands.length === 0 ? (
          <p className="ag-command-empty">
            {text(
              `没有匹配“${query.trim()}”的页面或仓库。`,
              `No page or repository matches “${query.trim()}”.`,
            )}
          </p>
        ) : (
          commands.map((command, index) => (
            <div key={command.id} role="presentation">
              {(index === 0 || commands[index - 1].group !== command.group) && (
                <div className="ag-command-group" role="presentation">
                  {groupLabels[command.group]}
                </div>
              )}
              {/* Keyboard selection runs through the combobox input via
                  aria-activedescendant; options are pointer targets only. */}
              {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events */}
              <div
                id={optionId(index)}
                role="option"
                tabIndex={-1}
                aria-selected={index === active}
                className="ag-command-option"
                data-active={index === active ? "true" : "false"}
                onMouseMove={() => setActive(index)}
                onClick={() => runCommand(command)}
              >
                <span className="ag-command-icon" aria-hidden="true">
                  {command.icon}
                </span>
                <span className="ag-command-label">{command.label}</span>
                {command.meta && (
                  <span className="ag-command-meta">{command.meta}</span>
                )}
              </div>
            </div>
          ))
        )}
      </div>
      <div className="ag-command-footer" aria-hidden="true">
        <span>
          <kbd>↑</kbd>
          <kbd>↓</kbd>
          {text("选择", "Select")}
        </span>
        <span>
          <kbd>⏎</kbd>
          {text("打开", "Open")}
        </span>
        <span>
          <kbd>esc</kbd>
          {text("关闭", "Close")}
        </span>
      </div>
    </Modal>
  );
}
