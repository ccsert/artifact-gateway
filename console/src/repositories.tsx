import { FormEvent, useCallback, useEffect, useState } from 'react';
import { createRepository, deleteRepository, getRepository, listArtifacts, listRepositories, type Artifact, type Format, type Repository, type RepositoryPage } from './client';
import { client } from './client/client.gen';

client.setConfig({
  baseUrl: import.meta.env.VITE_GATEWAY_API_URL ?? '/api/v2',
});

const operationsBaseUrl = import.meta.env.VITE_GATEWAY_OPERATIONS_URL ?? '/api/v1';

type CacheStatus = {
  object_count: number;
  bytes: number;
  pending_candidates: number;
  last_completed_at?: string;
  last_error?: string;
  successful_runs: number;
  failed_runs: number;
};

function authHeaders(): Record<string, string> {
  const token = window.localStorage.getItem('gatewayAdminToken');
  return token ? { Authorization: `Bearer ${token}` } : {};
}

function problemMessage(error: unknown) {
  return error instanceof Error ? error.message : 'The repository service could not complete this request.';
}

function dataOrThrow<T>(result: { data?: T; error?: unknown }) {
  if (result.error) throw new Error(typeof result.error === 'object' && result.error && 'message' in result.error && typeof result.error.message === 'string' ? result.error.message : 'The repository service could not complete this request.');
  if (result.data === undefined) throw new Error('The repository service returned no data.');
  return result.data;
}

function repositoryPageOrThrow(result: { data?: RepositoryPage; error?: { message?: string } }) {
  const page = dataOrThrow<RepositoryPage>(result);
  if (!Array.isArray(page.items)) throw new Error('The repository service returned an invalid repository inventory.');
  return page;
}

async function operationsError(response: Response) {
  const text = await response.text();
  if (!text) return `Gateway returned HTTP ${response.status}.`;
  try {
    const body = JSON.parse(text) as { message?: string; detail?: string };
    return body.message ?? body.detail ?? text;
  } catch {
    return text;
  }
}

async function getCacheStatus() {
  const response = await fetch(`${operationsBaseUrl}/operations/cache`, { headers: authHeaders() });
  if (!response.ok) throw new Error(await operationsError(response));
  return await response.json() as CacheStatus;
}

async function collectCache() {
  const response = await fetch(`${operationsBaseUrl}/operations/cache/collect`, { method: 'POST', headers: authHeaders() });
  if (!response.ok) throw new Error(await operationsError(response));
}

function numberLabel(value: number) {
  return new Intl.NumberFormat().format(value);
}

function timeLabel(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never';
}

export function App() {
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [selected, setSelected] = useState<Repository | null>(null);

  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [cacheStatus, setCacheStatus] = useState<CacheStatus | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [cacheBusy, setCacheBusy] = useState(false);

  const refreshCache = useCallback(async () => {
    setCacheBusy(true); setError('');
    try { setCacheStatus(await getCacheStatus()); }
    catch (reason) { setError(problemMessage(reason)); }
    finally { setCacheBusy(false); }
  }, []);

  const refresh = useCallback(async () => {
    setBusy(true); setError('');
    try {
      const page = repositoryPageOrThrow(await listRepositories({ headers: authHeaders() }));
      setRepositories(page.items);
      if (selected) {
        const repository = dataOrThrow<Repository>(await getRepository({ path: { repositoryId: selected.id }, headers: authHeaders() }));
        setSelected(repository);
        setArtifacts(repository.format === 'maven' ? dataOrThrow<{ items: Artifact[] }>(await listArtifacts({ path: { repositoryId: repository.id }, headers: authHeaders() })).items : []);
      }
    } catch (reason) { setError(problemMessage(reason)); }
    finally { setBusy(false); }
  }, [selected]);

  useEffect(() => { void refresh(); void refreshCache(); }, []); // Initial inventory and operations state.

  useEffect(() => {
    if (!selected || selected.format !== 'maven') return;
    void (async () => {
      try { setArtifacts(dataOrThrow<{ items: Artifact[] }>(await listArtifacts({ path: { repositoryId: selected.id }, headers: authHeaders() })).items); }
      catch (reason) { setError(problemMessage(reason)); }
    })();
  }, [selected?.id, selected?.format]);

  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    try {
      const created = dataOrThrow<Repository>(await createRepository({ body: { name, format }, headers: { ...authHeaders(), 'Idempotency-Key': crypto.randomUUID() } }));
      setName(''); setSelected(created); await refresh();
    } catch (reason) { setError(problemMessage(reason)); }
    finally { setBusy(false); }
  }

  async function disable() {
    if (!selected) return;
    setBusy(true); setError('');
    try { dataOrThrow(await deleteRepository({ path: { repositoryId: selected.id }, headers: authHeaders() })); setSelected(await dataOrThrow<Repository>(await getRepository({ path: { repositoryId: selected.id }, headers: authHeaders() }))); await refresh(); }
    catch (reason) { setError(problemMessage(reason)); }
    finally { setBusy(false); }
  }

  async function runCacheCollection() {
    setCacheBusy(true); setError('');
    try { await collectCache(); setCacheStatus(await getCacheStatus()); }
    catch (reason) { setError(problemMessage(reason)); }
    finally { setCacheBusy(false); }
  }

  return <main className="shell">
    <header><div><p className="eyebrow">Artifact Gateway</p><h1>Repositories</h1></div><button className="icon-button" aria-label="Refresh repositories" title="Refresh repositories" onClick={() => { void refresh(); void refreshCache(); }} disabled={busy || cacheBusy}>↻</button></header>
    {error && <div className="alert" role="alert">{error}</div>}
    <section className="operations" aria-label="Cache operations">
      <div>
        <h2>Cache operations</h2>
        <dl>
          <dt>Objects</dt><dd>{cacheStatus ? numberLabel(cacheStatus.object_count) : '—'}</dd>
          <dt>Bytes</dt><dd>{cacheStatus ? numberLabel(cacheStatus.bytes) : '—'}</dd>
          <dt>Pending GC</dt><dd>{cacheStatus ? numberLabel(cacheStatus.pending_candidates) : '—'}</dd>
          <dt>Successful runs</dt><dd>{cacheStatus ? numberLabel(cacheStatus.successful_runs) : '—'}</dd>
          <dt>Failed runs</dt><dd>{cacheStatus ? numberLabel(cacheStatus.failed_runs) : '—'}</dd>
          <dt>Last completed</dt><dd>{timeLabel(cacheStatus?.last_completed_at)}</dd>
        </dl>
        {cacheStatus?.last_error && <p className="cache-error">{cacheStatus.last_error}</p>}
      </div>
      <button type="button" onClick={() => void runCacheCollection()} disabled={cacheBusy}>Collect cache</button>
    </section>
    <section className="workspace" aria-label="Repository management">
      <form className="create" onSubmit={submit}>
        <h2>Create repository</h2>
        <label>Name<input required minLength={1} maxLength={63} value={name} onChange={(event) => setName(event.target.value)} placeholder="release-artifacts" /></label>
        <label>Format<select value={format} onChange={(event) => setFormat(event.target.value as Format)}><option value="oci">OCI</option><option value="maven">Maven</option><option value="raw">Raw</option><option value="conan">Conan (read-through)</option></select></label>
        <button type="submit" disabled={busy}>Create repository</button>
      </form>
      <section className="inventory"><h2>Repository inventory</h2><div className="table" role="table" aria-label="Repository inventory">
        <div className="row heading" role="row"><span>Name</span><span>Format</span><span>State</span></div>
        {repositories.map((repository) => <button className="row repository" role="row" key={repository.id} onClick={() => { setSelected(repository); setArtifacts([]); }}><span>{repository.name}</span><span>{repository.format.toUpperCase()}</span><span className={'state ' + repository.state}>{repository.state === 'deleting' ? 'Disabled' : repository.state}</span></button>)}
        {!busy && repositories.length === 0 && <p className="empty">No repositories yet.</p>}
      </div></section>
      <aside className="detail" aria-live="polite"><h2>Repository details</h2>{selected ? <><dl><dt>Name</dt><dd>{selected.name}</dd><dt>Format</dt><dd>{selected.format.toUpperCase()}</dd><dt>State</dt><dd>{selected.state === 'deleting' ? 'Disabled' : selected.state}</dd><dt>Version</dt><dd>{selected.version}</dd></dl>{selected.format === 'maven' && <><h3>Published coordinates</h3>{artifacts.length ? <ul className="artifact-list">{artifacts.map((artifact) => <li key={artifact.id}><strong>{artifact.coordinate}</strong><small>{artifact.state} · {artifact.digest} · {new Date(artifact.createdAt).toLocaleString()}</small></li>)}</ul> : <p className="empty">No published coordinates.</p>}</>}{selected.state === 'active' && <button className="danger" onClick={() => void disable()} disabled={busy}>Disable repository</button>}</> : <p className="empty">Select a repository to inspect its configuration.</p>}</aside>
    </section>
  </main>;
}
