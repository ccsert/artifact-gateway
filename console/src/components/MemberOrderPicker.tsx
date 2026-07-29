import type { Repository } from '../client';

// 成员排序选择器：左侧候选仓库（点击添加），右侧已选成员（上下移动调序、移除）。
// 比"按点击顺序编号"更直观——顺序就是列表顺序，可直接调整。

export function MemberOrderPicker({
  candidates,
  memberIds,
  onChange,
}: {
  candidates: Repository[];
  memberIds: string[];
  onChange: (ids: string[]) => void;
}) {
  const repoName = (id: string) => candidates.find((r) => r.id === id)?.name ?? id.slice(0, 8) + '…';
  const available = candidates.filter((r) => !memberIds.includes(r.id));

  const move = (index: number, dir: -1 | 1) => {
    const next = [...memberIds];
    const target = index + dir;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  const remove = (id: string) => onChange(memberIds.filter((x) => x !== id));
  const add = (id: string) => onChange([...memberIds, id]);

  return (
    <div className="grid grid-cols-2 gap-3">
      {/* 候选 */}
      <div>
        <div className="mb-1.5 text-[10px] uppercase tracking-wider text-zinc-500">可添加（{available.length}）</div>
        <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-zinc-800 p-1.5">
          {available.length === 0 && (
            <div className="px-2 py-4 text-center text-xs text-zinc-600">全部已加入</div>
          )}
          {available.map((r) => (
            <button
              key={r.id}
              type="button"
              onClick={() => add(r.id)}
              className="flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-xs text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
            >
              <span className="font-mono">{r.name}</span>
              <span className="text-zinc-600">+</span>
            </button>
          ))}
        </div>
      </div>
      {/* 已选（有序） */}
      <div>
        <div className="mb-1.5 text-[10px] uppercase tracking-wider text-zinc-500">
          成员顺序（{memberIds.length}，自上而下解析）
        </div>
        <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-zinc-800 p-1.5">
          {memberIds.length === 0 && (
            <div className="px-2 py-4 text-center text-xs text-zinc-600">从左侧点击添加成员</div>
          )}
          {memberIds.map((id, i) => (
            <div
              key={id}
              className="flex items-center gap-1 rounded-md bg-cyan-500/5 px-2 py-1.5"
            >
              <span className="w-5 shrink-0 text-center font-mono text-[10px] text-cyan-400">{i + 1}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-200" title={repoName(id)}>
                {repoName(id)}
              </span>
              <button
                type="button"
                onClick={() => move(i, -1)}
                disabled={i === 0}
                title="上移"
                className="rounded p-0.5 text-zinc-500 hover:bg-zinc-700 hover:text-zinc-200 disabled:opacity-30"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="m18 15-6-6-6 6"/></svg>
              </button>
              <button
                type="button"
                onClick={() => move(i, 1)}
                disabled={i === memberIds.length - 1}
                title="下移"
                className="rounded p-0.5 text-zinc-500 hover:bg-zinc-700 hover:text-zinc-200 disabled:opacity-30"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="m6 9 6 6 6-6"/></svg>
              </button>
              <button
                type="button"
                onClick={() => remove(id)}
                title="移除"
                className="rounded p-0.5 text-zinc-500 hover:bg-rose-500/20 hover:text-rose-300"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 6 6 18M6 6l12 12"/></svg>
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
