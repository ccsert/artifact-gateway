import { listFormatProfiles } from "../client";
import type { Format, FormatProfile } from "../client";

let cachedProfiles: Promise<FormatProfile[]> | undefined;

export function loadFormatProfiles(): Promise<FormatProfile[]> {
  if (!cachedProfiles) {
    cachedProfiles = listFormatProfiles().then(({ data, error }) => {
      if (error) throw error;
      if (!data) throw new Error("Format profile response is empty");
      return data.items;
    });
    void cachedProfiles.catch(() => {
      cachedProfiles = undefined;
    });
  }
  return cachedProfiles;
}

export function repositoryFormats(
  profiles: FormatProfile[],
  repositoryType?: "hosted" | "proxy",
): Format[] {
  return profiles
    .filter(
      (profile) =>
        !repositoryType || profile.repositoryTypes.includes(repositoryType),
    )
    .map((profile) => profile.format);
}

export function repositoryTypes(
  profiles: FormatProfile[],
): ("hosted" | "proxy")[] {
  return (["hosted", "proxy"] as const).filter((repositoryType) =>
    profiles.some((profile) =>
      profile.repositoryTypes.includes(repositoryType),
    ),
  );
}

export function groupFormats(profiles: FormatProfile[]): Format[] {
  return profiles
    .filter((profile) => profile.groupSupported)
    .map((profile) => profile.format);
}

export function resetFormatProfilesCacheForTests() {
  cachedProfiles = undefined;
}
