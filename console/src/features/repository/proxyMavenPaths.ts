/**
 * A warm request's repository path: a Maven GAV
 * (groupId:artifactId:version[:extension[:classifier]]) or a path as typed.
 */
export function mavenWarmPath(input: string): string | null {
  const value = input.trim();
  if (!value) return null;
  if (value.includes("/")) return value.replace(/^\/+/, "");
  const parts = value.split(":");
  if (parts.length < 3) return null;
  const [groupId, artifactId, version, extension = "jar", classifier] = parts;
  const suffix = classifier ? `-${classifier}` : "";
  return `${groupId.replaceAll(".", "/")}/${artifactId}/${version}/${artifactId}-${version}${suffix}.${extension}`;
}
