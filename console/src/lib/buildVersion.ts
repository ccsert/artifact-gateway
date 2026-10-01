// Vite injects the Console package version only into built bundles. Development
// and older bundles without the constant have no known Console build identity.
export function getConsoleBuildVersion(): string {
  return typeof __CONSOLE_BUILD_VERSION__ === "string"
    ? __CONSOLE_BUILD_VERSION__.trim() || "dev"
    : "dev";
}

export function buildVersionsMismatch(
  consoleVersion: string,
  gatewayVersion: string,
): boolean {
  const normalize = (version: string) => version.trim().replace(/^v/, "");
  const known = (version: string) =>
    version !== "" && !["dev", "unknown"].includes(version.toLowerCase());
  const consoleBuild = normalize(consoleVersion);
  const gatewayBuild = normalize(gatewayVersion);
  return (
    known(consoleBuild) && known(gatewayBuild) && consoleBuild !== gatewayBuild
  );
}
