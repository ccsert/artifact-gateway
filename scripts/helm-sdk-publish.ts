import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { basename } from "node:path";
import { argv, env, exit, versions } from "node:process";
import type { PublishHelmPreviewChartData } from "../console/src/client/types.gen.ts";
import { formDataBodySerializer } from "../console/src/client/core/bodySerializer.gen.ts";

const [url, chartPath, provenancePath, receiptPath] = argv.slice(2);
if (
  !url ||
  !chartPath ||
  !provenancePath ||
  !receiptPath ||
  !env.HELM_SDK_BEARER
) {
  throw new Error("owned Helm SDK fixture inputs are required");
}
const chart = await readFile(chartPath);
const provenance = await readFile(provenancePath);
const body: PublishHelmPreviewChartData["body"] = {
  chart: new File([new Uint8Array(chart)], basename(chartPath)),
  provenance: new File([new Uint8Array(provenance)], basename(provenancePath)),
};
const form = formDataBodySerializer.bodySerializer(body);
const serializedChart = form.get("chart");
const serializedProvenance = form.get("provenance");
if (
  !(serializedChart instanceof File) ||
  !(serializedProvenance instanceof File) ||
  serializedChart.name !== basename(chartPath) ||
  serializedProvenance.name !== basename(provenancePath)
) {
  throw new Error("generated SDK serializer lost named binary files");
}
const response = await fetch(url, {
  method: "PUT",
  headers: {
    Authorization: "Bearer " + env.HELM_SDK_BEARER,
    "Idempotency-Key": "owned-helm-publication",
  },
  body: form,
});
const receipt = {
  nodeVersion: versions.node,
  status: response.status,
  response: await response.json(),
  chart: {
    filename: serializedChart.name,
    size: chart.length,
    sha256: createHash("sha256").update(chart).digest("hex"),
  },
  provenance: {
    filename: serializedProvenance.name,
    size: provenance.length,
    sha256: createHash("sha256").update(provenance).digest("hex"),
  },
};
await writeFile(receiptPath, JSON.stringify(receipt, null, 2) + "\n", {
  mode: 0o600,
});
if (response.status !== 201) exit(1);
