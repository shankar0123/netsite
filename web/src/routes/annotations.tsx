// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  listAnnotations,
  type Annotation,
  type AnnotationListFilter,
} from "../api/client";

// What: /annotations list view with the four LIST filters the API
// exposes — scope, scope_id, from, to. v0.0.18 scope; the create form
// (POST /v1/annotations) lands in v0.0.19 alongside the canary detail
// "annotate at this timestamp" affordance.
//
// How: filter state is local React state; the URLSearchParams build
// happens in `listAnnotations()`. Filters debounce-by-blur — the
// query refires only when the user clicks "Apply", because the
// from/to inputs are typed and we don't want a fetch per keystroke.
//
// Why: annotations are a low-traffic, high-value surface (incident
// timeline pins). Polishing this list view to be filterable from
// day one means an operator can find "what did we say about the
// last route leak" without grepping JSON.

const SCOPES: Annotation["scope"][] = ["canary", "pop", "test"];

export function AnnotationsPage() {
  const [filter, setFilter] = useState<AnnotationListFilter>({});
  const [draft, setDraft] = useState<AnnotationListFilter>({});
  const q = useQuery({
    queryKey: ["annotations", filter],
    queryFn: () => listAnnotations(filter),
  });

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Annotations</h1>
        <p className="text-sm text-zinc-400">
          Free-form markdown notes pinned to a canary, POP, or test.
          Immutable once written — there is no PATCH; corrections are
          new annotations that supersede.
        </p>
      </header>

      <FilterBar
        draft={draft}
        setDraft={setDraft}
        onApply={() => setFilter(draft)}
        onClear={() => {
          setDraft({});
          setFilter({});
        }}
      />

      {q.isPending ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : q.isError ? (
        <p className="text-sm text-red-400">Failed to load annotations.</p>
      ) : q.data.length === 0 ? (
        <p className="text-sm text-zinc-500">
          No annotations match this filter. POST one to{" "}
          <code className="font-mono">/v1/annotations</code>.
        </p>
      ) : (
        <ul className="space-y-3">
          {q.data.map((a) => (
            <AnnotationRow key={a.id} a={a} />
          ))}
        </ul>
      )}
    </div>
  );
}

function FilterBar({
  draft,
  setDraft,
  onApply,
  onClear,
}: {
  draft: AnnotationListFilter;
  setDraft: (f: AnnotationListFilter) => void;
  onApply: () => void;
  onClear: () => void;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onApply();
      }}
      className="grid gap-3 rounded-md border border-zinc-800 bg-zinc-900/40 p-4 sm:grid-cols-5"
    >
      <label className="text-xs text-zinc-400 sm:col-span-1">
        Scope
        <select
          value={draft.scope ?? ""}
          onChange={(e) =>
            setDraft({
              ...draft,
              scope: (e.target.value || undefined) as Annotation["scope"] | undefined,
            })
          }
          className="mt-1 w-full rounded bg-zinc-950 border border-zinc-800 px-2 py-1 text-sm font-mono text-zinc-200"
        >
          <option value="">any</option>
          {SCOPES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </label>

      <label className="text-xs text-zinc-400 sm:col-span-2">
        Scope ID
        <input
          type="text"
          value={draft.scope_id ?? ""}
          onChange={(e) =>
            setDraft({ ...draft, scope_id: e.target.value || undefined })
          }
          placeholder="e.g. tst-https-api or pop-lhr-01"
          className="mt-1 w-full rounded bg-zinc-950 border border-zinc-800 px-2 py-1 text-sm font-mono text-zinc-200"
        />
      </label>

      <label className="text-xs text-zinc-400">
        From
        <input
          type="datetime-local"
          value={draft.from ?? ""}
          onChange={(e) =>
            setDraft({ ...draft, from: e.target.value || undefined })
          }
          className="mt-1 w-full rounded bg-zinc-950 border border-zinc-800 px-2 py-1 text-sm font-mono text-zinc-200"
        />
      </label>

      <label className="text-xs text-zinc-400">
        To
        <input
          type="datetime-local"
          value={draft.to ?? ""}
          onChange={(e) =>
            setDraft({ ...draft, to: e.target.value || undefined })
          }
          className="mt-1 w-full rounded bg-zinc-950 border border-zinc-800 px-2 py-1 text-sm font-mono text-zinc-200"
        />
      </label>

      <div className="sm:col-span-5 flex gap-2 justify-end">
        <button
          type="button"
          onClick={onClear}
          className="rounded border border-zinc-800 px-3 py-1 text-xs text-zinc-400 hover:bg-zinc-900"
        >
          Clear
        </button>
        <button
          type="submit"
          className="rounded bg-sky-600 px-3 py-1 text-xs font-medium text-white hover:bg-sky-500"
        >
          Apply
        </button>
      </div>
    </form>
  );
}

function AnnotationRow({ a }: { a: Annotation }) {
  return (
    <li className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
      <div className="flex items-baseline justify-between gap-2">
        <span className="inline-block rounded bg-zinc-800 px-1.5 py-0.5 text-[10px] font-mono text-zinc-300">
          {a.scope}:{a.scope_id}
        </span>
        <time className="font-mono text-[10px] text-zinc-500" dateTime={a.at}>
          {new Date(a.at).toLocaleString()}
        </time>
      </div>
      <pre className="mt-2 whitespace-pre-wrap text-sm text-zinc-200 font-sans">
        {a.body_md}
      </pre>
      <p className="mt-2 text-[10px] text-zinc-500 font-mono">
        by {a.author_id}
        {a.created_at !== a.at ? ` · created ${new Date(a.created_at).toLocaleString()}` : ""}
      </p>
    </li>
  );
}
