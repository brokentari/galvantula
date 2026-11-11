import { useEffect, useRef, useState, type ReactNode } from "react";
import type { CritStatus, DecideRequest, GateComment, Snapshot } from "./gen/events";
import type { RunView } from "./state";
import { ContractView } from "./ContractView";
import { TaskTable } from "./TaskTable";
import { contractLines, parseBlocks } from "./Markdown";
import { loadDraft, saveDraft, type Draft } from "./drafts";
import "./gate.css";

const bytes = (s: string) => new TextEncoder().encode(s).length;

// bodyProblem says how to fix a decision the server would refuse, or "" if it is fine.
// The limit is maxBody in internal/server, which caps the whole request body.
function bodyProblem(notes: string, comments: GateComment[]): string {
  const body: DecideRequest = { approved: false, notes, comments };
  return bytes(JSON.stringify(body)) > 1 << 20 ? "Shorten the notes or the comments. Together they must be under 1 MB." : "";
}

// The limit is MaxCommentBytes in internal/event.
const commentProblem = (body: string) =>
  body.trim() === "" ? "Write a comment, or press Cancel." : bytes(body) > 4000 ? "Shorten the comment. The limit is 4,000 bytes." : "";

const list = (ids: string[]) => (ids.length > 1 ? `${ids.slice(0, -1).join(", ")} and ${ids[ids.length - 1]}` : ids.join(""));
const count = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;
const endOf = (c: GateComment) => c.end_line ?? c.line;
const where = (c: GateComment) => (endOf(c) > c.line ? `Lines ${c.line} to ${endOf(c)}` : `Line ${c.line}`);
const empty: Draft = { notes: "", comments: [] };

// A removed button must not drop focus, so it goes back to the line's number.
const focusGutter = (line: number) =>
  setTimeout(() => document.querySelector<HTMLElement>(`.md-line[data-line="${line}"] .md-gutter`)?.focus());

function CommentForm({ line, endLine, initial, onSave, onCancel }: { line: number; endLine: number; initial: string; onSave: (body: string) => void; onCancel: () => void }) {
  const [body, setBody] = useState(initial);
  const [error, setError] = useState("");
  const field = useRef<HTMLTextAreaElement>(null);
  useEffect(() => field.current?.focus(), []);
  const id = `comment-${line}`;
  function save() {
    const problem = commentProblem(body);
    setError(problem);
    if (problem) field.current?.focus();
    else onSave(body);
  }
  return (
    <div
      className="gate-thread gate-form"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onCancel();
        }
      }}
    >
      <label htmlFor={id}>{endLine > line ? `Comment on lines ${line} to ${endLine}` : `Comment on line ${line}`}</label>
      <textarea
        id={id}
        ref={field}
        aria-describedby={`${id}-error`}
        aria-invalid={error !== ""}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onBlur={() => setError(commentProblem(body))}
        rows={3}
      />
      <p id={`${id}-error`} className="error">
        {error}
      </p>
      <div className="gate-buttons">
        <button type="button" className="approve" onClick={save}>
          Save comment
        </button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

function Saved({ c, busy, onEdit, onDelete }: { c: GateComment; busy: boolean; onEdit?: () => void; onDelete: () => void }) {
  return (
    <div className="gate-thread gate-comment">
      <p className="muted">{where(c)}</p>
      <p className="gate-body">{c.body}</p>
      <div className="gate-buttons">
        {onEdit && (
          <button type="button" disabled={busy} aria-label={`Edit comment on ${where(c).toLowerCase()}`} onClick={onEdit}>
            Edit
          </button>
        )}
        <button type="button" disabled={busy} aria-label={`Delete comment on ${where(c).toLowerCase()}`} onClick={onDelete}>
          Delete
        </button>
      </div>
    </div>
  );
}

type Work = string[] | "loading" | "failed";
type Open = { line: number; endLine: number; index?: number }; // the comment form: a new comment, or the one at index

export function GateReview({ run, id, live, onSelect }: { run: RunView; id: string; live: boolean; onSelect: (id: string) => void }) {
  const node = run.graph.nodes.find((n) => n.id === id);
  const view = run.nodes[id];
  const waiting = view?.status === "waiting";
  const brief = view?.brief;
  const round = brief?.round ?? 0;
  const contract = brief?.contract;
  const changes = brief?.changes;
  const layout = !!(contract || brief?.tasks?.length); // else: an old log, or a gate after a tool node
  const [draft, setDraft] = useState(() => loadDraft(run.id, id, round));
  const [notesError, setNotesError] = useState("");
  const [sending, setSending] = useState<boolean>(); // the decision on its way to the server: true approves
  const [error, setError] = useState("");
  const [work, setWork] = useState<Work>("loading");
  const [open, setOpen] = useState<Open>();
  const [status, setStatus] = useState(""); // the one aria-live message
  const [showChanges, setShowChanges] = useState(false);
  const [crit, setCrit] = useState(false); // the server has Crit
  const [critBusy, setCritBusy] = useState(false);
  const [critURL, setCritURL] = useState("");

  // The draft is per round: load the one for this gate and round. Saving happens in change().
  useEffect(() => {
    setDraft(loadDraft(run.id, id, round));
    setOpen(undefined);
  }, [run.id, id, round]);

  // Load again each time the gate waits, so a send-back shows the new work.
  useEffect(() => {
    if (!live || !waiting || layout) return;
    let stale = false;
    setWork("loading");
    fetch(`/api/runs/${run.id}/state`)
      .then((res) => (res.ok ? (res.json() as Promise<Snapshot>) : Promise.reject(new Error(res.statusText))))
      .then((snap) => !stale && setWork(snap.gates?.find((g) => g.node === id)?.context ?? []))
      .catch(() => !stale && setWork("failed"));
    return () => {
      stale = true;
    };
  }, [run.id, id, live, waiting, layout]);

  // An old server answers 404: show no Crit button.
  useEffect(() => {
    setCrit(false);
    setCritURL("");
    if (!live || !waiting || !contract) return;
    let stale = false;
    fetch(`/api/runs/${run.id}/gates/${id}/crit`)
      .then(async (res) => {
        if (stale || !res.ok) return;
        setCrit(true);
        setCritURL(((await res.json()) as CritStatus).url ?? "");
      })
      .catch(() => {});
    return () => {
      stale = true;
    };
  }, [run.id, id, live, waiting, round, !!contract]);

  if (!node || !view) return null;
  const deps = node.deps ?? [];
  const sources = list(deps);

  if (!waiting) {
    const d = view.decision;
    if (!d) return null;
    const [word, cls] = d.approved ? ["Approved", "ok"] : d.revise ? ["Sent back", "retry"] : ["Rejected", "bad"];
    return (
      <div className="gate-review">
        <p>
          <strong className={`decision-${cls}`}>{word}</strong> <span className="muted">{new Date(d.at).toLocaleTimeString()}</span>
        </p>
        {d.notes && <pre className="gate-notes">{d.notes}</pre>}
        {d.comments?.map((c, i) => (
          <div key={i} className="gate-thread gate-comment">
            <p className="muted">{where(c)}</p>
            {c.quote && <pre className="gate-notes">{c.quote}</pre>}
            <p className="gate-body">{c.body}</p>
          </div>
        ))}
      </div>
    );
  }

  const cur = live ? draft : empty; // replay shows no draft
  const hasComments = cur.comments.length > 0;
  const change = (d: Draft) => {
    setDraft(d);
    saveDraft(run.id, id, round, d);
  };
  const next = run.graph.nodes.filter((n) => n.deps?.includes(id)).map((n) => n.id);
  const approveLine = next.length > 3 ? `${next.slice(0, 2).join(", ")} and ${next.length - 2} more` : list(next);
  const left = view.revisionsLeft;
  const hasNotes = cur.notes.trim() !== "";
  const feedback = [hasNotes && "your notes", hasComments && count(cur.comments.length, "comment")].filter(Boolean).join(" and ");
  const rejectLine =
    left === undefined
      ? "Reject without notes ends the run."
      : left === 0
        ? "Reject ends the run."
        : feedback
          ? `Reject sends this back to ${sources} with ${feedback}. ${count(left, "revision")} left.`
          : contract
            ? `Reject ends the run. Add notes or a comment to send this back to ${sources}.`
            : `Reject ends the run. Add notes to send this back to ${sources}.`;

  async function decide(approved: boolean) {
    const comments = cur.comments.map(({ line, end_line, body }) => ({ line, ...(end_line && end_line > line ? { end_line } : {}), body }));
    const problem = bodyProblem(cur.notes, comments);
    setNotesError(problem);
    if (problem) return;
    setSending(approved);
    setError("");
    const body: DecideRequest = { approved, notes: cur.notes, ...(comments.length ? { comments } : {}) };
    try {
      const res = await fetch(`/api/runs/${run.id}/gates/${id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (res.ok) {
        change(empty);
        setOpen(undefined);
      }
      // 409 means the gate or the run moved on, and this page did not see it.
      else if (res.status === 409) setError(`${(await res.text()).trim()}. Reload the page to see the run as it is now.`);
      else if (res.status === 422) setError(`${(await res.text()).trim().replace(/\.?$/, ".")} Fix the comment, then try again.`);
      else setError((await res.text()) || res.statusText);
    } catch {
      setError("The server did not answer. Start it with factory serve, then try again.");
    } finally {
      setSending(undefined);
    }
  }

  async function openCrit() {
    setCritBusy(true);
    setStatus("");
    try {
      const res = await fetch(`/api/runs/${run.id}/gates/${id}/crit`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
      if (!res.ok) throw new Error(res.statusText);
      setCritURL(((await res.json()) as CritStatus).url ?? "");
    } catch {
      setStatus("Crit did not start. Run crit --version in a terminal, then try again.");
    } finally {
      setCritBusy(false);
    }
  }

  async function useCritComments() {
    let got: CritStatus;
    try {
      const res = await fetch(`/api/runs/${run.id}/gates/${id}/crit`);
      if (!res.ok) throw new Error(res.statusText);
      got = await res.json();
    } catch {
      setStatus("Could not read Crit. Press Use Crit comments to try again.");
      return;
    }
    const fresh = (got.comments ?? []).filter((c) => !cur.comments.some((d) => d.line === c.line && endOf(d) === endOf(c) && d.body === c.body));
    const notes = (got.notes ?? "").trim();
    const addNotes = notes !== "" && !cur.notes.includes(notes);
    if (fresh.length === 0 && !addNotes)
      return setStatus(got.comments?.length || notes ? "Your draft already has every Crit comment." : "Crit has no comments yet. Add one in Crit, then press Use Crit comments.");
    change({ notes: addNotes ? (cur.notes.trim() ? `${cur.notes}\n\n${notes}` : notes) : cur.notes, comments: [...cur.comments, ...fresh].sort((a, b) => a.line - b.line) });
    setStatus(fresh.length > 0 ? `Added ${count(fresh.length, "comment")} from Crit.` : "Added the notes from Crit.");
  }

  const blocks = contract ? parseBlocks(contractLines(contract.markdown)) : [];
  const inBlock = (c: GateComment) => blocks.some((b) => c.line >= b.line && c.line <= b.endLine);
  const lost = changes?.earlier.filter((e) => e.line === 0) ?? [];
  const earlierLabel = `Round ${round - 1}`;

  function threads(line: number, endLine: number): ReactNode {
    const items: ReactNode[] = [];
    changes?.earlier.forEach((e, i) => {
      if (e.line < line || e.line > endLine) return;
      items.push(
        <div key={`e${i}`} className="gate-thread gate-earlier">
          <p className="muted">{earlierLabel} · these lines did not change</p>
          <pre className="gate-notes">{e.quote}</pre>
          <p className="gate-body">{e.body}</p>
        </div>,
      );
    });
    cur.comments.forEach((c, i) => {
      if (c.line < line || c.line > endLine) return;
      if (open?.index === i)
        items.push(
          <CommentForm key={`f${i}`} line={line} endLine={endLine} initial={c.body} onCancel={() => close(line)} onSave={(body) => {
            change({ ...cur, comments: cur.comments.map((x, j) => (j === i ? { ...x, body } : x)) });
            setStatus(`Comment saved on line ${c.line}.`);
            close(line);
          }} />,
        );
      else items.push(<Saved key={`c${i}`} c={c} busy={sending !== undefined} onEdit={() => setOpen({ line, endLine, index: i })} onDelete={() => remove(i, line)} />);
    });
    if (open && open.index === undefined && open.line === line)
      items.push(
        <CommentForm key="new" line={line} endLine={endLine} initial="" onCancel={() => close(line)} onSave={(body) => {
          change({ ...cur, comments: [...cur.comments, { line, ...(endLine > line ? { end_line: endLine } : {}), body }].sort((a, b) => a.line - b.line) });
          setStatus(`Comment saved on line ${line}.`);
          close(line);
        }} />,
      );
    return items.length > 0 ? items : null;
  }

  function close(line: number) {
    setOpen(undefined);
    focusGutter(line);
  }

  function remove(i: number, line: number) {
    change({ ...cur, comments: cur.comments.filter((_, j) => j !== i) });
    setStatus("Comment deleted.");
    focusGutter(line);
  }

  const counts = contract && `${count(contract.tests, "test")} · ${contract.red} fail on the stubs`;

  return (
    <div className="gate-review">
      {layout && brief && (
        <>
          <section className="gate-check">
            <h3>What to check.</h3>
            <ul>
              {brief.check.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
            {counts && <p className="gate-counts">{counts}</p>}
          </section>
          {brief.tasks && <TaskTable tasks={brief.tasks} />}
          {contract && (
            <>
              {(changes || (live && crit)) && (
                <div className="gate-tools">
                  {changes && (
                    <button type="button" aria-pressed={showChanges} onClick={() => setShowChanges(!showChanges)}>
                      {showChanges ? "Hide changes" : "Show changes"}
                    </button>
                  )}
                  {live && crit && (
                    <>
                      <button type="button" aria-busy={critBusy} disabled={critBusy} onClick={openCrit}>
                        {critBusy ? "Starting Crit…" : "Open in Crit"}
                      </button>
                      {critURL && (
                        <>
                          <a href={critURL} target="_blank" rel="noreferrer">
                            Open Crit
                          </a>
                          <button type="button" onClick={useCritComments}>
                            Use Crit comments
                          </button>
                        </>
                      )}
                    </>
                  )}
                </div>
              )}
              {lost.length > 0 && (
                <section className="gate-lost">
                  <h3>Your round {round - 1} comments on lines the architect changed</h3>
                  <ul>
                    {lost.map((e, i) => (
                      <li key={i}>
                        <pre className="gate-notes">{e.quote}</pre>
                        <p className="gate-body">{e.body}</p>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              <ContractView
                contract={contract}
                changes={changes}
                showChanges={showChanges}
                onComment={live && sending === undefined ? (line, endLine) => setOpen({ line, endLine }) : undefined}
                threads={threads}
              />
              {cur.comments.some((c) => !inBlock(c)) && (
                <section className="gate-lost">
                  <h3>Comments on lines with no text</h3>
                  {cur.comments.map((c, i) => !inBlock(c) && <Saved key={i} c={c} busy={sending !== undefined} onDelete={() => remove(i, c.line)} />)}
                </section>
              )}
            </>
          )}
        </>
      )}
      <p className="gate-status" aria-live="polite">
        {status}
      </p>
      {!layout && live && deps.length > 0 && (
        <section className="gate-work">
          <h3>
            From{" "}
            {deps.map((d, i) => (
              <span key={d}>
                {i > 0 && ", "}
                <button className="link" onClick={() => onSelect(d)}>
                  {d}
                </button>
              </span>
            ))}
          </h3>
          {work === "loading" ? (
            <p className="muted">Loading…</p>
          ) : work === "failed" ? (
            <p className="error">Could not load this. Open {sources} to read its log.</p>
          ) : work.length === 0 ? (
            <p className="muted">{sources} logged nothing. Open it before you decide.</p>
          ) : (
            <pre className="gate-context">{work.join("\n")}</pre>
          )}
        </section>
      )}
      <fieldset className="gate" disabled={sending !== undefined || !live}>
        {!live && <p className="muted">Go back to live to decide.</p>}
        <label htmlFor="gate-notes">Notes</label>
        <textarea
          id="gate-notes"
          aria-describedby="gate-notes-error"
          aria-invalid={notesError !== ""}
          value={cur.notes}
          onChange={(e) => change({ ...cur, notes: e.target.value })}
          onBlur={() => setNotesError(bodyProblem(cur.notes, cur.comments))}
          rows={4}
        />
        <p id="gate-notes-error" className="error" aria-live="polite">
          {notesError}
        </p>
        <div id="gate-effects" className="gate-effects" aria-live="polite">
          {next.length > 0 && <p>Approve starts {approveLine}.</p>}
          {brief?.approve && <p>{brief.approve}</p>}
          {hasComments && <p>Approve keeps your {count(cur.comments.length, "comment")} in the log. The architect does not see them.</p>}
          <p>{rejectLine}</p>
        </div>
        <div className="gate-buttons">
          <button className="approve" aria-describedby="gate-effects" aria-busy={sending === true} onClick={() => decide(true)}>
            {sending === true ? "Approving…" : "Approve"}
          </button>
          <button className="reject" aria-describedby="gate-effects" aria-busy={sending === false} onClick={() => decide(false)}>
            {sending === false ? "Rejecting…" : "Reject"}
          </button>
        </div>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </fieldset>
    </div>
  );
}
