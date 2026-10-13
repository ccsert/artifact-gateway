import { repositoryGrantsZhCN } from "./repository-grants.zh-CN";
import { repositoryGrantsEnUS } from "./repository-grants.en-US";
import { accessZhCN } from "./access.zh-CN";
import { accessEnUS } from "./access.en-US";
import { identityZhCN } from "./identity.zh-CN";
import { identityEnUS } from "./identity.en-US";
import { publicBrowseZhCN } from "./public-browse.zh-CN";
import { publicBrowseEnUS } from "./public-browse.en-US";
import { shellEnUS } from "./shell.en-US";
import { shellZhCN } from "./shell.zh-CN";

/** Every Console message, merged from the feature dictionaries. */
export const zhCN = {
  ...publicBrowseZhCN,
  ...repositoryGrantsZhCN,
  ...accessZhCN,
  ...identityZhCN,
  ...shellZhCN,
};

export type MessageKey = keyof typeof zhCN;

export const enUS: Record<MessageKey, string> = {
  ...publicBrowseEnUS,
  ...repositoryGrantsEnUS,
  ...accessEnUS,
  ...identityEnUS,
  ...shellEnUS,
};
