import { uiZhCN } from "./ui.zh-CN";
import { uiEnUS } from "./ui.en-US";
import { libZhCN } from "./lib.zh-CN";
import { libEnUS } from "./lib.en-US";
import { formatsZhCN } from "./formats.zh-CN";
import { formatsEnUS } from "./formats.en-US";
import { artifactDetailZhCN } from "./artifact-detail.zh-CN";
import { artifactDetailEnUS } from "./artifact-detail.en-US";
import { repositoryZhCN } from "./repository.zh-CN";
import { repositoryEnUS } from "./repository.en-US";
import { auditZhCN } from "./audit.zh-CN";
import { auditEnUS } from "./audit.en-US";
import { dashboardZhCN } from "./dashboard.zh-CN";
import { dashboardEnUS } from "./dashboard.en-US";
import { searchZhCN } from "./search.zh-CN";
import { searchEnUS } from "./search.en-US";
import { operationsZhCN } from "./operations.zh-CN";
import { operationsEnUS } from "./operations.en-US";
import { migrationZhCN } from "./migration.zh-CN";
import { migrationEnUS } from "./migration.en-US";
import { nexusBatchZhCN } from "./nexus-batch.zh-CN";
import { nexusBatchEnUS } from "./nexus-batch.en-US";
import { nexusScheduleZhCN } from "./nexus-schedule.zh-CN";
import { nexusScheduleEnUS } from "./nexus-schedule.en-US";
import { systemZhCN } from "./system.zh-CN";
import { systemEnUS } from "./system.en-US";
import { groupsZhCN } from "./groups.zh-CN";
import { groupsEnUS } from "./groups.en-US";
import { settingsZhCN } from "./settings.zh-CN";
import { settingsEnUS } from "./settings.en-US";
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
  ...uiZhCN,
  ...libZhCN,
  ...formatsZhCN,
  ...artifactDetailZhCN,
  ...repositoryZhCN,
  ...auditZhCN,
  ...dashboardZhCN,
  ...searchZhCN,
  ...operationsZhCN,
  ...migrationZhCN,
  ...nexusBatchZhCN,
  ...nexusScheduleZhCN,
  ...systemZhCN,
  ...groupsZhCN,
  ...settingsZhCN,
  ...publicBrowseZhCN,
  ...repositoryGrantsZhCN,
  ...accessZhCN,
  ...identityZhCN,
  ...shellZhCN,
};

export type MessageKey = keyof typeof zhCN;

export const enUS: Record<MessageKey, string> = {
  ...uiEnUS,
  ...libEnUS,
  ...formatsEnUS,
  ...artifactDetailEnUS,
  ...repositoryEnUS,
  ...auditEnUS,
  ...dashboardEnUS,
  ...searchEnUS,
  ...operationsEnUS,
  ...migrationEnUS,
  ...nexusBatchEnUS,
  ...nexusScheduleEnUS,
  ...systemEnUS,
  ...groupsEnUS,
  ...settingsEnUS,
  ...publicBrowseEnUS,
  ...repositoryGrantsEnUS,
  ...accessEnUS,
  ...identityEnUS,
  ...shellEnUS,
};
