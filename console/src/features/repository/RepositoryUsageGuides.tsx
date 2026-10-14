import { Button } from "antd";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

export function CopyButton({ text: value }: { text: string }) {
  const { t } = usePreferences();
  const { copiedValue, copy } = useClipboardAction();
  const copied = copiedValue === value;
  return (
    <Button
      type="text"
      size="small"
      onClick={() => void copy(value)}
      className="shrink-0"
    >
      {copied ? t("repository.copied") : t("repository.copy")}
    </Button>
  );
}

export function RepositorySnippetBlock({
  label,
  code,
}: {
  label: string;
  code: string;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2">
      <div className="mb-1 flex items-center justify-between gap-3">
        <span className="text-xs uppercase tracking-wider text-fg-tertiary">
          {label}
        </span>
        <CopyButton text={code} />
      </div>
      <code className="block whitespace-pre-wrap break-all font-mono text-xs leading-5 text-[var(--ag-content-secondary)]">
        {code}
      </code>
    </div>
  );
}

export function OCIPublishGuide({ repoName }: { repoName: string }) {
  const { t } = usePreferences();
  const registry = window.location.host;
  const image = `${registry}/${repoName}/<image>:<tag>`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {t("repository.publishWithDockerOrPodman")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {t("repository.askAnAdministratorForAServiceAccountCredential")}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label={t("repository.signInToRegistry")}
          code={`printf '%s' "$ARTIFACT_GATEWAY_TOKEN" | docker login ${registry} --username publisher --password-stdin`}
        />
        <RepositorySnippetBlock
          label={t("repository.tagAndPush")}
          code={`docker tag <local-image>:<tag> ${image}\ndocker push ${image}`}
        />
      </div>
    </div>
  );
}

export function NpmPublishGuide({ repoName }: { repoName: string }) {
  const { t } = usePreferences();
  const registry = `${window.location.origin}/npm/${repoName}/`;
  const authPath = `//${window.location.host}/npm/${repoName}/:_authToken=\${ARTIFACT_GATEWAY_TOKEN}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {t("repository.configureNpmRegistry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {t("repository.useAGatewayAPIKeyOrResolverToken")}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label=".npmrc"
          code={`registry=${registry}\n${authPath}`}
        />
        <RepositorySnippetBlock
          label={t("repository.publish")}
          code={`npm publish --registry ${registry}`}
        />
      </div>
    </div>
  );
}

export function PyPIPublishGuide({ repoName }: { repoName: string }) {
  const { t } = usePreferences();
  const base = `${window.location.origin}/pypi/${repoName}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {t("repository.configurePyPIRepository")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {t("repository.anonymousPipReadsNeedNoCredentialsTwineUses")}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label="pip"
          code={`pip config set global.index-url ${base}/simple/`}
        />
        <RepositorySnippetBlock
          label=".pypirc"
          code={`[distutils]\nindex-servers = ${repoName}\n\n[${repoName}]\nrepository = ${base}/legacy/\nusername = resolver\npassword = \${GATEWAY_RESOLVER_TOKEN}`}
        />
        <RepositorySnippetBlock
          label={t("repository.publish")}
          code={`twine upload --repository-url ${base}/legacy/ dist/*`}
        />
      </div>
    </div>
  );
}

export function CargoPublishGuide({ repoName }: { repoName: string }) {
  const { t } = usePreferences();
  const index = `sparse+${window.location.origin}/cargo/${repoName}/`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {t("repository.configureCargoRegistry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {t("repository.askAnAdministratorForRepositoryWriteAccessFor")}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label=".cargo/config.toml"
          code={`[registries.gateway]\nindex = "${index}"\ncredential-provider = "cargo:token"`}
        />
        <RepositorySnippetBlock
          label={t("repository.publishAndRead")}
          code={
            "cargo publish --registry gateway\ncargo search <crate> --registry gateway\ncargo add <crate> --registry gateway\ncargo install <crate> --registry gateway"
          }
        />
        <RepositorySnippetBlock
          label={t("repository.yankAndRestoreAVersion")}
          code={
            "cargo yank <crate>@<version> --registry gateway\ncargo yank --undo <crate>@<version> --registry gateway"
          }
        />
      </div>
    </div>
  );
}
