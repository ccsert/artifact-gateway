// TLS fixture for the live source-management browser gate. This is never
// imported by the Console application. Trust its test CA explicitly in the
// isolated Gateway and allow only this fixture hostname's private address.
import https from "node:https";
import fs from "node:fs";
import process from "node:process";
import { Buffer } from "node:buffer";
import { URL } from "node:url";

const origin = process.env.NEXUS_FIXTURE_ORIGIN ?? "https://nexus-fixture:8443";
const repositories = [
  { name: "releases", format: "maven2", type: "hosted" },
  { name: "raw-source", format: "raw", type: "hosted" },
  { name: "snapshots", format: "maven2", type: "hosted" },
  { name: "docker-hosted", format: "docker", type: "hosted" },
];
https
  .createServer(
    {
      cert: fs.readFileSync(process.env.NEXUS_FIXTURE_CERT),
      key: fs.readFileSync(process.env.NEXUS_FIXTURE_KEY),
    },
    (request, response) => {
      const answer = (value, status = 200) => {
        response.writeHead(status, { "Content-Type": "application/json" });
        response.end(JSON.stringify(value));
      };
      if (request.method !== "GET")
        return answer({ error: "read_only_fixture" }, 405);
      const credentials = Buffer.from(
        (request.headers.authorization ?? "").replace(/^Basic /, ""),
        "base64",
      ).toString();
      if (
        ![
          "fixture-reader:fixture-password",
          "fixture-reader:fixture-password-rotated",
        ].includes(credentials)
      )
        return answer({ error: "credentials_required" }, 401);
      const url = new URL(request.url, origin);
      if (url.pathname === "/service/rest/v1/repositories")
        return answer(repositories);
      const configuration =
        /^\/service\/rest\/v1\/repositories\/(maven|raw)\/hosted\/([^/]+)$/.exec(
          url.pathname,
        );
      if (configuration) {
        const name = configuration[2];
        return answer({
          name,
          ...(configuration[1] === "maven"
            ? {
                maven: {
                  versionPolicy: name === "snapshots" ? "SNAPSHOT" : "MIXED",
                },
              }
            : {}),
        });
      }
      if (url.pathname === "/service/rest/v1/assets") {
        const repository = url.searchParams.get("repository");
        const path =
          repository === "releases"
            ? "com/acme/library/1.0+build/library-1.0+build.jar"
            : "exports/build+1.txt";
        return answer({
          items: [
            {
              repository,
              format: repository === "releases" ? "maven2" : "raw",
              path,
              downloadUrl: `${origin}/repository/${repository}/${path.replaceAll("+", "%2B")}`,
              fileSize: repository === "releases" ? 2048 : 512,
              checksum: { sha256: "a".repeat(64) },
            },
          ],
          continuationToken: null,
        });
      }
      // Any artifact download is a test failure: this source slice reads metadata.
      return answer({ error: "unexpected_source_path" }, 404);
    },
  )
  .listen(Number(process.env.NEXUS_FIXTURE_PORT ?? 8443), "0.0.0.0");
