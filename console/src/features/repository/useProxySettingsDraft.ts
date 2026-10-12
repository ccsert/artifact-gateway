import { useState } from "react";
import { testEgressProxy } from "../../client";
import type {
  EgressProxyTestResult,
  EgressProxyWritable,
  Repository,
  UpstreamAuthWritable,
} from "../../client";

/**
 * A proxy repository's upstream drafts: endpoint, allowed hosts, egress
 * proxy and upstream authentication, with the request body they submit.
 */
export function useProxySettingsDraft(
  repo: Repository,
  setError: (error: unknown) => void,
) {
  const [endpoint, setEndpoint] = useState(repo.endpoint ?? "");
  const [hosts, setHosts] = useState((repo.allowedHosts ?? []).join(", "));
  const egress = repo.egressProxy;
  const [egressMode, setEgressMode] = useState<
    "direct" | "environment" | "custom"
  >(egress?.mode ?? "environment");
  const [egressProtocol, setEgressProtocol] = useState<"http" | "socks5">(
    egress?.protocol ?? "http",
  );
  const [egressHost, setEgressHost] = useState(egress?.host ?? "");
  const [egressPort, setEgressPort] = useState<number | null>(
    egress?.port ?? null,
  );
  const [egressUsername, setEgressUsername] = useState(egress?.username ?? "");
  const [egressPassword, setEgressPassword] = useState("");
  const [egressClearCredentials, setEgressClearCredentials] = useState(false);
  const [egressRemoteDns, setEgressRemoteDns] = useState(
    egress?.remoteDns ?? false,
  );
  const [egressNoProxy, setEgressNoProxy] = useState(
    (egress?.noProxy ?? []).join(", "),
  );
  const [egressTesting, setEgressTesting] = useState(false);
  const [egressTestResult, setEgressTestResult] =
    useState<EgressProxyTestResult | null>(null);

  const [upstreamScheme, setUpstreamScheme] = useState<
    "none" | "basic" | "bearer"
  >(repo.upstreamAuth?.scheme ?? "none");
  const [upstreamUsername, setUpstreamUsername] = useState(
    repo.upstreamAuth?.username ?? "",
  );
  const [upstreamSecret, setUpstreamSecret] = useState("");

  const reset = () => {
    setEndpoint(repo.endpoint ?? "");
    setHosts((repo.allowedHosts ?? []).join(", "));
    setEgressMode(repo.egressProxy?.mode ?? "environment");
    setEgressProtocol(repo.egressProxy?.protocol ?? "http");
    setEgressHost(repo.egressProxy?.host ?? "");
    setEgressPort(repo.egressProxy?.port ?? null);
    setEgressUsername(repo.egressProxy?.username ?? "");
    setEgressPassword("");
    setEgressClearCredentials(false);
    setEgressRemoteDns(repo.egressProxy?.remoteDns ?? false);
    setEgressNoProxy((repo.egressProxy?.noProxy ?? []).join(", "));
    setEgressTestResult(null);
    setUpstreamScheme(repo.upstreamAuth?.scheme ?? "none");
    setUpstreamUsername(repo.upstreamAuth?.username ?? "");
    setUpstreamSecret("");
  };

  const buildEgressProxyBody = (): EgressProxyWritable => {
    if (egressMode !== "custom") {
      return { mode: egressMode };
    }
    const noProxy = egressNoProxy
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    return {
      mode: "custom",
      protocol: egressProtocol,
      host: egressHost.trim(),
      port: egressPort ?? 0,
      ...(egressUsername.trim() ? { username: egressUsername.trim() } : {}),
      ...(egressPassword ? { password: egressPassword } : {}),
      ...(egressClearCredentials ? { clearCredentials: true } : {}),
      remoteDns: egressProtocol === "socks5" ? egressRemoteDns : false,
      noProxy,
    };
  };

  const buildUpstreamAuthBody = (): UpstreamAuthWritable => {
    if (upstreamScheme === "none") {
      return { scheme: "none" };
    }
    return {
      scheme: upstreamScheme,
      ...(upstreamScheme === "basic" && upstreamUsername.trim()
        ? { username: upstreamUsername.trim() }
        : {}),
      ...(upstreamSecret ? { secret: upstreamSecret } : {}),
    };
  };

  const runEgressTest = async () => {
    setEgressTesting(true);
    setEgressTestResult(null);
    const { data, error: err } = await testEgressProxy({
      path: { repositoryId: repo.id },
    });
    setEgressTesting(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) setEgressTestResult(data);
  };

  /** The proxy part of a repository update. */
  const body = (withUpstreamAuth: boolean) => ({
    endpoint: endpoint.trim(),
    allowedHosts: hosts
      .split(",")
      .map((h) => h.trim())
      .filter(Boolean),
    egressProxy: buildEgressProxyBody(),
    ...(withUpstreamAuth ? { upstreamAuth: buildUpstreamAuthBody() } : {}),
  });

  return {
    endpoint,
    setEndpoint,
    hosts,
    setHosts,
    egressMode,
    setEgressMode,
    egressProtocol,
    setEgressProtocol,
    egressHost,
    setEgressHost,
    egressPort,
    setEgressPort,
    egressUsername,
    setEgressUsername,
    egressPassword,
    setEgressPassword,
    egressClearCredentials,
    setEgressClearCredentials,
    egressRemoteDns,
    setEgressRemoteDns,
    egressNoProxy,
    setEgressNoProxy,
    egressTesting,
    setEgressTesting,
    egressTestResult,
    setEgressTestResult,
    upstreamScheme,
    setUpstreamScheme,
    upstreamUsername,
    setUpstreamUsername,
    upstreamSecret,
    setUpstreamSecret,
    egress,
    reset,
    body,
    runEgressTest,
  };
}

export type ProxySettingsDraft = ReturnType<typeof useProxySettingsDraft>;
