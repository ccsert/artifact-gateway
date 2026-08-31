import { Button } from "antd";
import { useClipboardAction } from "../../components/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

export function CopyButton({ text }: { text: string }) {
  const { text: localize } = usePreferences();
  const { copiedValue, copy } = useClipboardAction();
  const copied = copiedValue === text;
  return (
    <Button
      type="text"
      size="small"
      onClick={() => void copy(text)}
      className="shrink-0"
    >
      {copied ? localize("已复制", "Copied") : localize("复制", "Copy")}
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
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <div className="mb-1 flex items-center justify-between gap-3">
        <span className="text-xs uppercase tracking-wider text-zinc-500">
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

export function NpmPublishGuide({ repoName }: { repoName: string }) {
  const { text } = usePreferences();
  const registry = `${window.location.origin}/npm/${repoName}/`;
  const authPath = `//${window.location.host}/npm/${repoName}/:_authToken=\${ARTIFACT_GATEWAY_TOKEN}`;
  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <div>
        <h3 className="text-sm font-medium text-zinc-100">
          {text("注册 npm 仓库", "Configure npm registry")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-zinc-500">
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
        <h3 className="text-sm font-medium text-zinc-100">
          {text("注册 PyPI 仓库", "Configure PyPI repository")}
        </h3>
        <p className="mt-1 text-xs leading-5 text-zinc-500">
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
