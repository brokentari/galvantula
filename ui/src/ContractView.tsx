import { Fragment, type ReactNode } from "react";
import type { Contract, ContractChanges } from "./gen/events";
import { Inline, contractLines, parseBlocks, type Block } from "./Markdown";
import "./contract.css";

// shortcut: a list is divs with a margin, not <ul>, and table rows are CSS grids, not a <table>,
// because a comment thread must fit between two rows. Upgrade if a screen reader check needs list or table roles.
function Body({ b }: { b: Block }) {
  switch (b.kind) {
    case "heading":
      return <div role="heading" aria-level={Math.min(b.level + 2, 6)}><Inline text={b.text} /></div>;
    case "item":
      return <div style={{ marginLeft: `${b.depth * 2}ch` }}>{/^\d/.test(b.marker) ? b.marker : "•"} <Inline text={b.text} /></div>;
    case "row":
      return (
        <div className={b.head ? "head" : undefined} style={{ gridTemplateColumns: `repeat(${b.cells.length}, minmax(0, 1fr))` }}>
          {b.cells.map((c, i) => <span key={i}><Inline text={c} /></span>)}
        </div>
      );
    case "rule":
      return null;
    case "code":
      return <>{b.text}</>;
    default:
      return <Inline text={b.text} />;
  }
}

export function ContractView({ contract, changes, showChanges, onComment, threads }: {
  contract: Contract;
  changes?: ContractChanges;
  showChanges: boolean;
  // Leave out onComment to show line numbers as text, as replay does.
  onComment?: (line: number, endLine: number) => void;
  // What to show under a block: its comments and the open form.
  threads?: (line: number, endLine: number) => ReactNode;
}) {
  const blocks = parseBlocks(contractLines(contract.markdown));
  const diff = showChanges ? changes : undefined;
  const added = new Set(diff?.added);
  const removed = diff?.removed ?? [];
  // A removed line goes before the first block that starts after its `after` line. Find it once per line.
  const slot = removed.map((r) => blocks.find((x) => x.line > r.after));
  const before = (b: Block) => removed.filter((_, i) => slot[i] === b);
  const tail = removed.filter((_, i) => !slot[i]);
  const gone = (r: { text: string }, key: string) => (
    <div className="md-line removed" key={key}>
      <span className="md-gutter" aria-hidden="true">−</span>
      <div className="md-block"><span className="sr-only">removed </span>{r.text}</div>
    </div>
  );

  const line = (b: Block) => {
    const label = b.endLine > b.line ? `Comment on lines ${b.line} to ${b.endLine}` : `Comment on line ${b.line}`;
    const isNew = added.size > 0 && [...added].some((n) => n >= b.line && n <= b.endLine);
    const t = threads?.(b.line, b.endLine);
    const open = t != null && t !== false && !(Array.isArray(t) && t.length === 0);
    const num = <>{isNew && <span aria-hidden="true">+</span>}{b.line}</>;
    return (
      <Fragment key={b.line}>
        {before(b).map((r, i) => gone(r, `r${b.line}.${i}`))}
        <div className={isNew ? "md-line added" : "md-line"} data-line={b.line}>
          {onComment ? (
            <button type="button" className="md-gutter" aria-label={label} aria-expanded={open} onClick={() => onComment(b.line, b.endLine)}>{num}</button>
          ) : (
            <span className="md-gutter">{num}</span>
          )}
          <div className={`md-block md-${b.kind}`}>{isNew && <span className="sr-only">new </span>}<Body b={b} /></div>
        </div>
        {open && t}
      </Fragment>
    );
  };

  const u = contract.unclear;
  const inU = (b: Block) => !!u && b.line >= u.line && b.line <= u.end_line;
  const rest = blocks.filter((b) => !inU(b));
  return (
    <div className="contract">
      <a className="skip" href="#gate-notes">Skip to your decision</a>
      {u && <section aria-label="Unclear in the spec">{blocks.filter(inU).map(line)}</section>}
      {(!u || rest.length > 0 || tail.length > 0) && (
        <section aria-label={u ? "The rest of the contract" : "The contract"}>
          {rest.map(line)}
          {tail.map((r, i) => gone(r, `t${i}`))}
        </section>
      )}
    </div>
  );
}
