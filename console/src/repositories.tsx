import { FormEvent, useCallback, useEffect, useState } from 'react';
import { createRepository, deleteRepository, getRepository, listArtifacts, listRepositories, type Artifact, type Repository, type RepositoryPage } from './client';
import { client } from './client/client.gen';

type Format = 'raw' | 'oci' | 'maven';

client.setConfig({
  baseUrl: import.meta.env.VITE_GATEWAY_API_URL ?? '/api/v2',
});

function authHeaders() {
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

export function App() {
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [selected, setSelected] = useState<Repository | null>(null);

  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    setBusy(true); setError('');
    try {
      const page = dataOrThrow<RepositoryPage>(await listRepositories({ headers: authHeaders() }));
      setRepositories(page.items);
      if (selected) {
        const repository = dataOrThrow<Repository>(await getRepository({ path: { repositoryId: selected.id }, headers: authHeaders() }));
        setSelected(repository);
        setArtifacts(repository.format === 'maven' ? dataOrThrow<{ items: Artifact[] }>(await listArtifacts({ path: { repositoryId: repository.id }, headers: authHeaders() })).items : []);
      }
    } catch (reason) { setError(problemMessage(reason)); }
    finally { setBusy(false); }
  }, [selected]);

  useEffect(() => { void refresh(); }, []); // Initial inventory.

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

  return <main className="shell">
    <header><div><p className="eyebrow">Artifact Gateway</p><h1>Repositories</h1></div><button className="icon-button" aria-label="Refresh repositories" title="Refresh repositories" onClick={() => void refresh()} disabled={busy}>↻</button></header>
    {error && <div className="alert" role="alert">{error}</div>}
    <section className="workspace" aria-label="Repository management">
      <form className="create" onSubmit={submit}>
        <h2>Create repository</h2>
        <label>Name<input required pattern="[a-z0-9][a-z0-9-]{0,61}" value={name} onChange={(event) => setName(event.target.value)} placeholder="release-artifacts" /></label>
        <label>Format<select value={format} onChange={(event) => setFormat(event.target.value as Format)}><option value="oci">OCI</option><option value="maven">Maven</option><option value="raw">Raw</option></select></label>
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
