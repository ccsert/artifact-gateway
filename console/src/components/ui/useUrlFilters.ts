import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

type Filters = Record<string, string>;

/**
 * A list's filters kept in the URL, so a filtered view survives a reload and
 * can be shared. Values equal to their default are left out of the URL, and a
 * value outside `allowed` (an old or hand-edited link) reads as the default.
 * One `set` call writes all the keys it is given, and changes replace the
 * history entry rather than adding one per keystroke.
 *
 * Declare `defaults` and `allowed` at module level so they stay stable.
 */
export function useUrlFilters<F extends Filters>(
  defaults: F,
  allowed: { [K in keyof F]?: readonly F[K][] } = {},
) {
  const [params, setParams] = useSearchParams();
  const values = useMemo(() => {
    const read = { ...defaults };
    for (const key of Object.keys(defaults) as (keyof F & string)[]) {
      const raw = params.get(key);
      const options = allowed[key];
      if (raw !== null && (!options || options.includes(raw as F[typeof key])))
        read[key] = raw as F[typeof key];
    }
    return read;
  }, [allowed, defaults, params]);

  const set = useCallback(
    (patch: Partial<F>) =>
      setParams(
        (current) => {
          const next = new URLSearchParams(current);
          for (const [key, value] of Object.entries(patch)) {
            if (value === undefined || value === defaults[key])
              next.delete(key);
            else next.set(key, value);
          }
          return next;
        },
        { replace: true },
      ),
    [defaults, setParams],
  );
  const reset = useCallback(() => set(defaults), [defaults, set]);
  const active = (Object.keys(defaults) as (keyof F)[]).some(
    (key) => values[key] !== defaults[key],
  );
  return { values, set, reset, active };
}
