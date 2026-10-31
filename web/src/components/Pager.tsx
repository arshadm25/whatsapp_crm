import { useState } from "react";
import { useTranslation } from "react-i18next";

export const PAGE_SIZES = [25, 50, 100] as const;
export type PageSize = (typeof PAGE_SIZES)[number];

// usePager walks a cursor-paged list one page at a time. It keeps the cursor of every page
// visited so Previous goes back without refetching from the start. Changing resetKey (the
// filters and page size) goes back to page 1.
export function usePager<C>(resetKey: string) {
  const [state, setState] = useState<{ key: string; cursors: (C | null)[]; index: number }>({ key: resetKey, cursors: [null], index: 0 });
  const s = state.key === resetKey ? state : { key: resetKey, cursors: [null] as (C | null)[], index: 0 };
  if (s !== state) setState(s);
  return {
    cursor: s.cursors[s.index],
    page: s.index + 1,
    next: (cursor: C) => setState({ key: s.key, cursors: [...s.cursors.slice(0, s.index + 1), cursor], index: s.index + 1 }),
    prev: () => setState({ ...s, index: Math.max(0, s.index - 1) }),
    first: () => setState({ ...s, index: 0 }),
  };
}

// Pager is the footer under a paged table: which rows are shown, page size, and
// first / previous / next buttons.
export function Pager({ page, size, shown, total, hasNext, busy, onPrev, onNext, onFirst, onSize }: {
  page: number;
  size: PageSize;
  shown: number;
  total?: number;
  hasNext: boolean;
  busy?: boolean;
  onPrev: () => void;
  onNext: () => void;
  onFirst: () => void;
  onSize: (s: PageSize) => void;
}) {
  const { t } = useTranslation();
  const from = shown ? (page - 1) * size + 1 : 0;
  const to = (page - 1) * size + shown;
  const pages = total != null ? Math.max(1, Math.ceil(total / size)) : undefined;
  return (
    <div className="cf pager">
      <span className="muted small">
        {total != null
          ? t("pager.rangeOf", { from: from.toLocaleString(), to: to.toLocaleString(), total: total.toLocaleString() })
          : t("pager.range", { from: from.toLocaleString(), to: to.toLocaleString() })}
      </span>
      <label className="muted small pager-size">
        {t("pager.perPage")}
        <select value={size} onChange={(e) => onSize(Number(e.target.value) as PageSize)}>
          {PAGE_SIZES.map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
      </label>
      <span className="pager-nav">
        {page > 2 && <button type="button" className="sm" onClick={onFirst} disabled={busy}>{t("pager.first")}</button>}
        <button type="button" className="sm" onClick={onPrev} disabled={busy || page <= 1} aria-label={t("pager.prev")}>‹ {t("pager.prev")}</button>
        <span className="small num-t">{pages ? t("pager.pageOf", { page, pages }) : t("pager.page", { page })}</span>
        <button type="button" className="sm" onClick={onNext} disabled={busy || !hasNext} aria-label={t("pager.next")}>{t("pager.next")} ›</button>
      </span>
    </div>
  );
}
