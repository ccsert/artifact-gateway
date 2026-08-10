import type { LifecycleJobDetails as LifecycleJobDetailsData } from "../client";
import { usePreferences } from "../lib/preferences";
import { CopyableValue } from "./ConsolePrimitives";

export function LifecycleJobDetails({
  details,
}: {
  details: LifecycleJobDetailsData;
}) {
  const { text } = usePreferences();
  return (
    <div className="grid max-w-5xl grid-cols-[max-content_minmax(0,1fr)] gap-x-5 gap-y-2 rounded-md border border-zinc-800/70 bg-zinc-950/20 px-4 py-3 text-xs">
      <span className="text-zinc-500">{text("格式", "Format")}</span>
      <span className="font-mono text-zinc-200">{details.format}</span>
      {details.sourceRepositoryId ? (
        <>
          <span className="text-zinc-500">
            {text("源仓库 ID", "Source repository ID")}
          </span>
          <CopyableValue
            value={details.sourceRepositoryId}
            label={`${details.sourceRepositoryId.slice(0, 8)}…`}
          />
        </>
      ) : null}
      <span className="text-zinc-500">{text("坐标", "Coordinate")}</span>
      <CopyableValue value={details.coordinate} />
      <span className="text-zinc-500">digest</span>
      <CopyableValue
        value={details.digest}
        label={`${details.digest.slice(0, 20)}…`}
      />
    </div>
  );
}
