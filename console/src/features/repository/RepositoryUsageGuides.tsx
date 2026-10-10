import { Button } from "antd";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

export function CopyButton({ text: value }: { text: string }) {
  const { text } = usePreferences();
  const { copiedValue, copy } = useClipboardAction();
  const copied = copiedValue === value;
  return (
    <Button
      type="text"
      size="small"
      onClick={() => void copy(value)}
      className="shrink-0"
    >
      {copied ? text("已复制", "Copied") : text("复制", "Copy")}
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
  const { text } = usePreferences();
  const registry = window.location.host;
  const image = `${registry}/${repoName}/<image>:<tag>`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {text("通过 Docker 或 Podman 发布", "Publish with Docker or Podman")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {text(
            "请管理员签发具有此仓库写入授权的服务账号凭据，并通过安全渠道提供给发布者。SSO 浏览器会话不能直接作为 Docker 凭据。",
            "Ask an administrator for a service-account credential with write access to this repository, delivered through a secure channel. The SSO browser session is not a Docker credential.",
          )}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label={text("登录 Registry", "Sign in to registry")}
          code={`printf '%s' "$ARTIFACT_GATEWAY_TOKEN" | docker login ${registry} --username publisher --password-stdin`}
        />
        <RepositorySnippetBlock
          label={text("标记与推送", "Tag and push")}
          code={`docker tag <local-image>:<tag> ${image}\ndocker push ${image}`}
        />
      </div>
    </div>
  );
}

export function NpmPublishGuide({ repoName }: { repoName: string }) {
  const { text } = usePreferences();
  const registry = `${window.location.origin}/npm/${repoName}/`;
  const authPath = `//${window.location.host}/npm/${repoName}/:_authToken=\${ARTIFACT_GATEWAY_TOKEN}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {text("注册 npm 仓库", "Configure npm registry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {text(
            "认证令牌使用 Gateway API Key 或 resolver token。",
            "Use a Gateway API key or resolver token for authentication.",
          )}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label=".npmrc"
          code={`registry=${registry}\n${authPath}`}
        />
        <RepositorySnippetBlock
          label={text("发布", "Publish")}
          code={`npm publish --registry ${registry}`}
        />
      </div>
    </div>
  );
}

export function PyPIPublishGuide({ repoName }: { repoName: string }) {
  const { text } = usePreferences();
  const base = `${window.location.origin}/pypi/${repoName}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {text("注册 PyPI 仓库", "Configure PyPI repository")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {text(
            "匿名仓库的 pip 读取无需凭据；twine 使用任意非空用户名和 resolver token。",
            "Anonymous pip reads need no credentials; twine uses any non-empty username with the resolver token.",
          )}
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
          label={text("发布", "Publish")}
          code={`twine upload --repository-url ${base}/legacy/ dist/*`}
        />
      </div>
    </div>
  );
}

export function CargoPublishGuide({ repoName }: { repoName: string }) {
  const { text } = usePreferences();
  const index = `sparse+${window.location.origin}/cargo/${repoName}/`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-fg-strong">
          {text("配置 Cargo 仓库", "Configure Cargo registry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-fg-tertiary">
          {text(
            "由管理员授予仓库写入权限。私有仓库和发布需要通过安全渠道注入 CARGO_REGISTRIES_GATEWAY_TOKEN，值为 Bearer 加 Gateway Token；不要写入配置文件。授权由仓库 Grant 管理，暂不支持 cargo owner。",
            "Ask an administrator for repository write access. For private reads and publishing, inject CARGO_REGISTRIES_GATEWAY_TOKEN as Bearer followed by the Gateway token through a secret manager; never put it in the config file. Repository grants manage access; cargo owner is unsupported.",
          )}
        </p>
      </div>
      <div className="space-y-3">
        <RepositorySnippetBlock
          label=".cargo/config.toml"
          code={`[registries.gateway]\nindex = "${index}"\ncredential-provider = "cargo:token"`}
        />
        <RepositorySnippetBlock
          label={text("发布与读取", "Publish and read")}
          code={
            "cargo publish --registry gateway\ncargo search <crate> --registry gateway\ncargo add <crate> --registry gateway\ncargo install <crate> --registry gateway"
          }
        />
        <RepositorySnippetBlock
          label={text("撤回与恢复版本", "Yank and restore a version")}
          code={
            "cargo yank <crate>@<version> --registry gateway\ncargo yank --undo <crate>@<version> --registry gateway"
          }
        />
      </div>
    </div>
  );
}
