import type { LibMessageKey } from "./lib.zh-CN";

export const libEnUS: Record<LibMessageKey, string> = {
  "lib.mavenRepositoryURL": "Maven repository URL",
  "lib.oCIRegistryAddress": "OCI registry address",
  "lib.dockerRegistrySetup": "Docker registry setup",
  "lib.conanRemoteAddress": "Conan remote address",
  "lib.npmRegistryURL": "npm registry URL",
  "lib.oneOffUsage": "One-off usage",
  "lib.cargoSparseIndex": "Cargo sparse index",
  "lib.searchAndInstallACrate": "Search and install a crate",
  "lib.aPTSourceURL": "APT source URL",
  "lib.downloadAnArtifact": "Download an artifact",
  "lib.dockerLoginImagePrefix":
    "docker login {host}\n# Image prefix: {host}/{repositoryName}/",
  "lib.mavenDependencyDynamicSnapshot": "Maven dependency (dynamic SNAPSHOT)",
  "lib.mavenDependencyPinnedBuild": "Maven dependency (pinned build)",
  "lib.buildPom": "Build POM",
  "lib.gradlePinnedBuild": "Gradle (pinned build)",
  "lib.mavenDependencyPomXml": "Maven dependency (pom.xml)",
  "lib.gradleDependency": "Gradle dependency",
  "lib.repositoryPath": "Repository path",
  "lib.dockerPull": "Docker pull",
  "lib.dockerRun": "Docker run",
  "lib.imageReference": "Image reference",
  "lib.addRemote": "Add remote",
  "lib.installPackage": "Install package",
  "lib.downloadUrl": "Download URL",
  "lib.curlDownload": "curl download",
  "lib.rawRepositoryAddress": "Raw repository address",
  "lib.failedToReadOciManifestStatus":
    "Failed to read the OCI manifest ({status})",
};
