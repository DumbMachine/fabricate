import {useEffect, useId, useRef, useState, type CSSProperties, type MouseEvent, type PointerEvent} from "react";
import {
  createSortedRowModel,
  flexRender,
  rowSortingFeature,
  sortFn_alphanumeric,
  tableFeatures,
  useTable,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";

import {
  DEFAULT_COLUMN_LIMIT,
  DEFAULT_ROW_LIMIT,
  PEEK_FETCH_TIMEOUT_MS,
  PEEK_SLOW_MS,
  collectionColumns,
  formatCell,
  limitedRows,
  loadScenarioPeek,
  missingPeekSource,
  officialScenarioSource,
  peekTitle,
  selectCollection,
  type PeekLoadFailure,
  type ScenarioCollection,
  type ScenarioPeekData,
} from "../../../packages/docs-content/resources/_components/scenario-peek";

export const client = "load";

type PeekRow = Record<string, unknown>;

const peekFeatures = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: {alphanumeric: sortFn_alphanumeric},
});

const SHEET_COLUMN_LIMIT = 16;
const SKELETON_COLUMNS = 5;
const SKELETON_ROWS = 8;
const SKELETON_WIDTHS = [72, 54, 66, 40, 58, 48, 70, 36];

type Props = {
  collection?: string;
  limit?: number;
  resource?: string;
  scenario?: string;
};

type Status =
  | {kind: "loading"; slow: boolean}
  | {kind: "error"; error: PeekLoadFailure}
  | {kind: "ready"; data: ScenarioPeekData};

function GitHubMark() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="18" height="18" fill="currentColor" aria-hidden="true">
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.87-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  );
}

function GitHubLink({href}: {href: string}) {
  return (
    <a className="scenario-peek__github" href={href} rel="noreferrer" target="_blank" aria-label="Open scenario source on GitHub">
      <GitHubMark />
    </a>
  );
}

function tableSlice(collection: ScenarioCollection, limit: number, columnLimit: number) {
  const rows = limitedRows(collection.rows, limit);
  const columns = collectionColumns(rows, columnLimit);
  const hidden = Math.max(0, collectionColumns(rows, Number.POSITIVE_INFINITY).length - columns.length);
  const shown = rows.length;
  const total = collection.rows.length;
  const countLabel = hidden > 0
    ? `Showing ${shown} of ${total} · ${hidden} more fields in source`
    : shown === total
      ? `${total} ${collection.key}`
      : `Showing ${shown} of ${total} ${collection.key}`;
  return {rows, columns, countLabel};
}

function PreviewTable({
  collection,
  limit,
  onOpen,
}: {
  collection: ScenarioCollection;
  limit: number;
  onOpen: () => void;
}) {
  const {rows, columns, countLabel} = tableSlice(collection, limit, DEFAULT_COLUMN_LIMIT);
  const pointer = useRef({x: 0, y: 0});

  const rememberPointer = (event: PointerEvent<HTMLDivElement>) => {
    pointer.current = {x: event.clientX, y: event.clientY};
  };

  const openFromClick = (event: MouseEvent<HTMLDivElement>) => {
    const dx = Math.abs(event.clientX - pointer.current.x);
    const dy = Math.abs(event.clientY - pointer.current.y);
    if (dx > 8 || dy > 8) {
      return;
    }
    onOpen();
  };

  return (
    <>
      <div
        className="scenario-peek__table-wrap scenario-peek__table-wrap--expandable"
        style={{"--scenario-peek-cols": Math.max(1, columns.length)} as CSSProperties}
        onPointerDown={rememberPointer}
        onClick={openFromClick}
      >
        <table className="scenario-peek__table">
          <caption>{collection.key}</caption>
          <thead>
            <tr>
              {columns.map((key) => (
                <th key={key} scope="col">{key}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={Math.max(1, columns.length)}>
                  <span className="scenario-peek__value">No seeded rows</span>
                </td>
              </tr>
            ) : (
              rows.map((row, index) => (
                <tr key={index}>
                  {columns.map((key) => (
                    <td key={key}>
                      <span className="scenario-peek__value">{formatCell(row[key])}</span>
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <p className="scenario-peek__footer">
        {countLabel}
        {" · "}
        <button className="scenario-peek__expand" type="button" onClick={onOpen}>
          Expand
        </button>
      </p>
    </>
  );
}

function SheetTable({
  collection,
  limit,
}: {
  collection: ScenarioCollection;
  limit: number;
}) {
  const {rows, columns, countLabel} = tableSlice(collection, limit, SHEET_COLUMN_LIMIT);
  const [sorting, setSorting] = useState<SortingState>([]);
  const columnDefs: ColumnDef<typeof peekFeatures, PeekRow>[] = columns.map((key) => ({
    id: key,
    accessorFn: (row) => formatCell(row[key]),
    header: key,
    sortFn: "alphanumeric",
  }));
  const table = useTable({
    features: peekFeatures,
    data: rows,
    columns: columnDefs,
    state: {sorting},
    onSortingChange: setSorting,
  });

  return (
    <>
      <div
        className="scenario-peek__table-wrap"
        style={{"--scenario-peek-cols": Math.max(1, columns.length)} as CSSProperties}
      >
        <table className="scenario-peek__table">
          <caption>{collection.key}</caption>
          <thead>
            {table.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => (
                  <th key={header.id} scope="col">
                    {header.isPlaceholder ? null : (
                      <button
                        className="scenario-peek__sort"
                        type="button"
                        onClick={header.column.getToggleSortingHandler()}
                      >
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {header.column.getIsSorted() === "asc"
                          ? " ↑"
                          : header.column.getIsSorted() === "desc"
                            ? " ↓"
                            : ""}
                      </button>
                    )}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.length === 0 ? (
              <tr>
                <td colSpan={Math.max(1, columns.length)}>
                  <span className="scenario-peek__value">No seeded rows</span>
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => (
                <tr key={row.id}>
                  {row.getAllCells().map((cell) => (
                    <td key={cell.id}>
                      <span className="scenario-peek__value">
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </span>
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <p className="scenario-peek__footer">{countLabel}</p>
    </>
  );
}

function CollectionTabs({
  collections,
  selected,
  onSelect,
}: {
  collections: ScenarioCollection[];
  selected: string | undefined;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="scenario-peek__tabs" role="tablist" aria-label="Seeded collections">
      {collections.map((entry) => (
        <button
          key={entry.key}
          className="scenario-peek__tab"
          type="button"
          role="tab"
          aria-selected={selected === entry.key}
          onClick={() => onSelect(entry.key)}
        >
          {entry.key} ({entry.rows.length})
        </button>
      ))}
    </div>
  );
}

function PeekSkeleton({slow}: {slow: boolean}) {
  const label = slow ? "Still loading scenario data…" : "Loading scenario data…";
  return (
    <div className="scenario-peek__placeholder">
      <div aria-hidden="true">
        <div className="scenario-peek__tabs">
          <span className="scenario-peek__tab scenario-peek__tab--skeleton" />
          <span className="scenario-peek__tab scenario-peek__tab--skeleton" />
          <span className="scenario-peek__tab scenario-peek__tab--skeleton" />
        </div>
        <div
          className="scenario-peek__table-wrap scenario-peek__table-wrap--skeleton"
          style={{"--scenario-peek-cols": SKELETON_COLUMNS} as CSSProperties}
        >
          <table className="scenario-peek__table">
            <thead>
              <tr>
                {Array.from({length: SKELETON_COLUMNS}, (_, index) => (
                  <th key={index} scope="col">
                    <span className="scenario-peek__skeleton-bar" style={{width: "42%"}} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {Array.from({length: SKELETON_ROWS}, (_, row) => (
                <tr key={row}>
                  {Array.from({length: SKELETON_COLUMNS}, (_, column) => (
                    <td key={column}>
                      <span
                        className="scenario-peek__skeleton-bar"
                        style={{width: `${SKELETON_WIDTHS[(row + column) % SKELETON_WIDTHS.length]}%`}}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <p className="scenario-peek__footer">{label}</p>
    </div>
  );
}

function PeekError({error, onRetry}: {error: PeekLoadFailure; onRetry: () => void}) {
  return (
    <div className="scenario-peek__placeholder scenario-peek__placeholder--error" role="alert">
      <p className="scenario-peek__error-title">{error.title}</p>
      <p className="scenario-peek__status">{error.message}</p>
      {error.retryable ? (
        <button className="scenario-peek__retry" type="button" onClick={onRetry}>
          Retry
        </button>
      ) : null}
    </div>
  );
}

export default function ScenarioPeek({
  collection,
  limit = DEFAULT_ROW_LIMIT,
  resource,
  scenario,
}: Props) {
  const resolved = resource && scenario ? officialScenarioSource(resource, scenario) : null;
  const rawURL = resolved?.rawURL;
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<Status>({kind: "loading", slow: false});
  const [active, setActive] = useState(collection);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!rawURL) {
      setStatus({kind: "error", error: missingPeekSource()});
      return;
    }
    const controller = new AbortController();
    setStatus({kind: "loading", slow: false});
    const slow = window.setTimeout(() => {
      setStatus((current) => current.kind === "loading" ? {kind: "loading", slow: true} : current);
    }, PEEK_SLOW_MS);
    const timeout = window.setTimeout(() => controller.abort("timeout"), PEEK_FETCH_TIMEOUT_MS);
    void loadScenarioPeek(rawURL, controller.signal).then((result) => {
      if (controller.signal.aborted && controller.signal.reason !== "timeout") {
        return;
      }
      if (!result) {
        return;
      }
      if (result.ok) {
        setActive((current) => current ?? selectCollection(result.data.collections, collection)?.key);
        setStatus({kind: "ready", data: result.data});
        return;
      }
      setStatus({kind: "error", error: result.error});
    });
    return () => {
      window.clearTimeout(slow);
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [attempt, collection, rawURL]);

  useEffect(() => {
    const node = dialog.current;
    if (!node) {
      return;
    }
    if (open && !node.open) {
      node.showModal();
    }
    if (!open && node.open) {
      node.close();
    }
  }, [open]);

  const selected = status.kind === "ready"
    ? selectCollection(status.data.collections, active)
    : null;
  const title = status.kind === "ready" ? status.data.id : peekTitle(resolved, scenario);

  return (
    <section className="scenario-peek not-prose" aria-busy={status.kind === "loading"} aria-label="Scenario data peek">
      <header className="scenario-peek__header">
        <div>
          <p className="scenario-peek__eyebrow">SCENARIO DATA</p>
          <p className="scenario-peek__title">{title}</p>
        </div>
        {resolved ? <GitHubLink href={resolved.blobURL} /> : null}
      </header>
      {status.kind === "loading" ? <PeekSkeleton slow={status.slow} /> : null}
      {status.kind === "error" ? (
        <PeekError error={status.error} onRetry={() => setAttempt((current) => current + 1)} />
      ) : null}
      {status.kind === "ready" ? (
        <>
          {status.data.scalars.length > 0 ? (
            <dl className="scenario-peek__scalars">
              {status.data.scalars.map((entry) => (
                <div key={entry.key}>
                  <dt>{entry.key}</dt>
                  <dd>{entry.value}</dd>
                </div>
              ))}
            </dl>
          ) : null}
          <CollectionTabs
            collections={status.data.collections}
            selected={selected?.key}
            onSelect={setActive}
          />
          {selected ? (
            <PreviewTable
              key={selected.key}
              collection={selected}
              limit={limit}
              onOpen={() => setOpen(true)}
            />
          ) : null}
          <dialog
            ref={dialog}
            className="scenario-peek__sheet"
            aria-labelledby={titleId}
            onClose={() => setOpen(false)}
            onClick={(event) => {
              if (event.target === dialog.current) {
                setOpen(false);
              }
            }}
          >
            <div className="scenario-peek__sheet-frame">
              <header className="scenario-peek__sheet-header">
                <div>
                  <p className="scenario-peek__eyebrow">SCENARIO DATA</p>
                  <h2 id={titleId} className="scenario-peek__title">{title}</h2>
                </div>
                <div className="scenario-peek__sheet-actions">
                  {resolved ? <GitHubLink href={resolved.blobURL} /> : null}
                  <button
                    className="scenario-peek__sheet-close"
                    type="button"
                    aria-label="Close sheet"
                    onClick={() => setOpen(false)}
                  >
                    Close
                  </button>
                </div>
              </header>
              <CollectionTabs
                collections={status.data.collections}
                selected={selected?.key}
                onSelect={setActive}
              />
              {selected ? (
                <SheetTable key={`sheet-${selected.key}`} collection={selected} limit={limit} />
              ) : null}
            </div>
          </dialog>
        </>
      ) : null}
    </section>
  );
}
