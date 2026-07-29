import type { Format } from '../client';

// 按格式生成制品的"使用方法"（可复制的接入方式）

export interface UsageSnippet {
  label: string;
  code: string;
}

function gatewayHost(): string {
  return window.location.host;
}
function gatewayBase(): string {
  return window.location.origin;
}

// Maven GAV 坐标 → 各用法
export function mavenUsage(repoName: string, coordinate: string): UsageSnippet[] {
  const parts = coordinate.split(':');
  const out: UsageSnippet[] = [];
  if (parts.length >= 3) {
    const [g, a, v] = parts;
    out.push({
      label: 'Maven 依赖 (pom.xml)',
      code: `<dependency>\n  <groupId>${g}</groupId>\n  <artifactId>${a}</artifactId>\n  <version>${v}</version>\n</dependency>`,
    });
    out.push({
      label: 'Gradle 依赖',
      code: `implementation '${g}:${a}:${v}'`,
    });
    const path = `${g.replaceAll('.', '/')}/${a}/${v}`;
    out.push({
      label: '仓库路径',
      code: `${gatewayBase()}/maven/${repoName}/${path}`,
    });
  }
  return out;
}

// OCI 镜像 → docker 命令
export function ociUsage(repoName: string, image: string, tag?: string): UsageSnippet[] {
  const ref = tag ? `${image}:${tag}` : image;
  const host = gatewayHost();
  return [
    { label: 'Docker 拉取', code: `docker pull ${host}/${repoName}/${ref}` },
    { label: 'Docker 运行', code: `docker run --rm ${host}/${repoName}/${ref}` },
    { label: '镜像引用', code: `${host}/${repoName}/${ref}` },
  ];
}

// Conan 引用 → conan 命令
export function conanUsage(repoName: string, reference: string): UsageSnippet[] {
  const base = gatewayBase();
  return [
    { label: '添加 remote', code: `conan remote add ${repoName} ${base}/conan/v2/${repoName}` },
    { label: '安装包', code: `conan install --requires=${reference} -r ${repoName}` },
  ];
}

// Raw 文件 → 下载
export function rawUsage(repoName: string, path: string): UsageSnippet[] {
  const url = `${gatewayBase()}/raw/${repoName}/${path}`;
  return [
    { label: '下载 URL', code: url },
    { label: 'curl 下载', code: `curl -O ${url}` },
  ];
}

export function usageFor(format: Format | string, repoName: string, coordinate: string, tag?: string): UsageSnippet[] {
  switch (format) {
    case 'maven':
      return mavenUsage(repoName, coordinate);
    case 'oci':
      return ociUsage(repoName, coordinate, tag);
    case 'conan':
      return conanUsage(repoName, coordinate);
    case 'raw':
      return rawUsage(repoName, coordinate);
    default:
      return [];
  }
}

// Maven 坐标 → group:artifact（用于版本聚合）
export function mavenGA(coordinate: string): string | null {
  const parts = coordinate.split(':');
  return parts.length >= 3 ? `${parts[0]}:${parts[1]}` : null;
}

// Maven 坐标 → version
export function mavenVersion(coordinate: string): string | null {
  const parts = coordinate.split(':');
  return parts.length >= 3 ? parts[2] : null;
}
