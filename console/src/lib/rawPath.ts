const rawPathSegmentSafeEscapes: Record<string, string> = {
  "%24": "$",
  "%26": "&",
  "%2B": "+",
  "%3A": ":",
  "%3D": "=",
  "%40": "@",
};

// Match Go's url.PathEscape so Raw coordinates have one stable wire identity.
export function encodeRawPathSegment(segment: string): string {
  return encodeURIComponent(segment)
    .replace(
      /[!'()*]/g,
      (character) => `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
    )
    .replace(
      /%(?:24|26|2B|3A|3D|40)/g,
      (escape) => rawPathSegmentSafeEscapes[escape] ?? escape,
    );
}

export function encodeRawPath(path: string): string {
  return path.split("/").map(encodeRawPathSegment).join("/");
}

export function rawResourceURL(
  repositoryName: string,
  canonicalPath: string,
): string {
  return `/raw/${encodeURIComponent(repositoryName)}/${canonicalPath}`;
}

// Raw APIs expose the canonical protocol coordinate. Decode only for
// presentation; callers must keep using the original coordinate for actions.
export function decodeRawPathForDisplay(path: string): string {
  try {
    return path.split("/").map(decodeURIComponent).join("/");
  } catch {
    return path;
  }
}

export function artifactCoordinateForDisplay(
  format: string,
  coordinate: string,
): string {
  return format === "raw" ? decodeRawPathForDisplay(coordinate) : coordinate;
}

// Console search boxes contain presentation text. Always encode their value,
// including literal percent signs, before sending the canonical API prefix.
export function canonicalRawSearchPrefix(value: string): string {
  return encodeRawPath(value);
}

export function containsDisallowedRawPathCharacters(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (
      codePoint <= 0x1f ||
      codePoint === 0x7f ||
      codePoint === 0x61c ||
      codePoint === 0x200e ||
      codePoint === 0x200f ||
      (codePoint >= 0x202a && codePoint <= 0x202e) ||
      (codePoint >= 0x2066 && codePoint <= 0x2069)
    ) {
      return true;
    }
  }
  return false;
}
