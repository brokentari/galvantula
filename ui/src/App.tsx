import { useEffect, useMemo, useRef, useState } from "react";
import type { FactoryEvent, RunSummary } from "./gen/events";
import { GraphView, type Marks } from "./GraphView";
import { NodePanel } from "./NodePanel";
import { RunStats } from "./RunStats";
import { Timeline } from "./Timeline";
import "./chrome.css";
import { apply, describe, diffGraphs, fold, type RunView } from "./state";
import type { Graph } from "./gen/events";
import { clock } from "./time";

const playStepMs = 350;
const startCommand = "factory start -fake fakes/factory.yaml graphs/factory.yaml";
const iconColor = { running: "#6cb6ff", succeeded: "#5fd08f", failed: "#ff8a80" };

function EmptyState() {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <div className="empty">
      <p>No runs yet. Start one:</p>
      <div className="empty-cmd">
        <code>{startCommand}</code>
        <button
          onClick={() =>
            navigator.clipboard.writeText(startCommand).then(
              () => setCopied(true),
              () => {}, // leave the label as it is
            )
          }
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </div>
  );
}

// useRuns returns undefined until the server has answered once.
function useRuns(): RunSummary[] | undefined {
  const [runs, setRuns] = useState<RunSummary[]>();
  useEffect(() => {
    let stop = false;
    const load = async () => {
      try {
        const res = await fetch("/api/runs");
        if (res.ok && !stop) setRuns((await res.json()) ?? []);
      } catch {
        // The server may be restarting; the next poll tries again.
      }
    };
    load();
    const t = setInterval(load, 3000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, []);
  return runs;
}

interface Stream {
  events: FactoryEvent[]; // grows in place; count changes with it
  count: number;
  live?: RunView; // the run after every event so far
}

// useEvents streams a run's events. EventSource reconnects by itself and
// sends Last-Event-ID, so the server resumes where the stream stopped. Each
// new event is applied once; only replay rebuilds the run from the start.
function useEvents(runId: string | undefined): Stream {
  // Appending in place keeps a long run's log cheap; copying the list for
  // each event would cost time that grows with the square of its length.
  const all = useRef<FactoryEvent[]>([]);
  const [stream, setStream] = useState<Stream>({ events: all.current, count: 0 });
  useEffect(() => {
    all.current = [];
    setStream({ events: all.current, count: 0 });
    if (!runId) return;
    const es = new EventSource(`/api/runs/${runId}/events`);
    es.onmessage = (msg) => {
      const e = JSON.parse(msg.data) as FactoryEvent;
      const last = all.current.at(-1);
      if (last && last.seq >= e.seq) return;
      all.current.push(e);
      setStream((s) => ({ events: all.current, count: all.current.length, live: apply(s.live, e) }));
    };
    return () => es.close();
  }, [runId]);
  return stream;
}

export function App() {
  const loaded = useRuns();
  const runs = loaded ?? [];
  const [chosen, setChosen] = useState(() => location.hash.slice(1));
  const runId = chosen || runs[0]?.id;
  const { events, count, live: liveRun } = useEvents(runId);
  const [cursor, setCursor] = useState<number | null>(null); // null means live
  const [playing, setPlaying] = useState(false);
  const [selected, setSelected] = useState<string>();
  const [version, setVersion] = useState<number | null>(null); // null means the latest graph

  // Live mode shows the growing list itself, so a new event copies nothing. count is a
  // dependency because events changes in place.
  const shown = useMemo(() => (cursor === null ? events : events.slice(0, cursor)), [events, count, cursor]);
  const run = useMemo(() => (cursor === null ? liveRun : fold(shown)), [cursor, liveRun, shown]);
  const live = cursor === null;

  useEffect(() => {
    if (!playing) return;
    const t = setInterval(() => {
      setCursor((c) => {
        const next = (c ?? 0) + 1;
        if (next >= events.length) setPlaying(false);
        return Math.min(next, events.length);
      });
    }, playStepMs);
    return () => clearInterval(t);
  }, [playing, events.length]);

  function pickRun(id: string) {
    location.hash = id;
    setChosen(id);
    setCursor(null);
    setPlaying(false);
    setSelected(undefined);
    setVersion(null);
  }

  // Looking at an earlier version shows what it changed: the tasks it added,
  // the ones it changed, and (as ghosts) the ones it took out.
  const { graph, marks }: { graph?: Graph; marks: Marks } = useMemo(() => {
    if (!run) return { marks: {} };
    const at = run.versions.findIndex((v) => v.version === version);
    if (at < 1) return { graph: run.graph, marks: {} };
    const d = diffGraphs(run.versions[at - 1].graph, run.versions[at].graph);
    const marks: Marks = {};
    for (const id of d.added) marks[id] = "added";
    for (const id of d.changed) marks[id] = "changed";
    for (const id of d.removed) marks[id] = "removed";
    const ghosts = run.versions[at - 1].graph.nodes.filter((n) => marks[n.id] === "removed");
    return { graph: { ...run.versions[at].graph, nodes: [...run.versions[at].graph.nodes, ...ghosts] }, marks };
  }, [run, version]);
  const replayEnd = live ? undefined : Date.parse(shown.at(-1)?.at ?? "") || undefined;

  // The tab shows the live run, so a person who replays still sees that a gate waits.
  const liveWaiting = liveRun ? liveRun.graph.nodes.filter((n) => liveRun.nodes[n.id]?.status === "waiting").map((n) => n.id) : [];
  const waitingKey = liveWaiting.join(",");
  const status = liveRun?.status;
  useEffect(() => {
    document.title = liveWaiting.length ? `(${liveWaiting.length}) ${liveWaiting[0]} waits · Galvantula` : "Galvantula";
    const fill = liveWaiting.length ? "#f5d547" : status ? iconColor[status] : "#6cb6ff";
    const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (icon) icon.href = `data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Ccircle cx='8' cy='8' r='6' fill='%23${fill.slice(1)}'/%3E%3C/svg%3E`;
  }, [waitingKey, status]);

  // Open a gate once when it starts to wait; the ref keeps a closed panel closed.
  const seenWaiting = useRef("");
  useEffect(() => {
    const was = seenWaiting.current;
    seenWaiting.current = waitingKey;
    // A gate that the person has decided gives way to the next one that waits.
    const free = !selected || (liveRun?.graph.nodes.find((n) => n.id === selected)?.kind === "gate" && liveRun.nodes[selected]?.status !== "waiting");
    if (live && waitingKey && waitingKey !== was && free) setSelected(waitingKey.split(",")[0]);
  }, [waitingKey]);

  const waiting = run ? run.graph.nodes.filter((n) => run.nodes[n.id]?.status === "waiting") : [];
  const recent = shown.slice(-40).reverse();

  return (
    <div className="app">
      <header className="top">
        <h1>Galvantula</h1>
        <select value={runId ?? ""} onChange={(e) => pickRun(e.target.value)} aria-label="Run">
          {runs.length === 0 && <option value="">no runs yet</option>}
          {runs.map((r) => (
            <option key={r.id} value={r.id}>
              {r.id} · {r.graph} · {r.status}
            </option>
          ))}
        </select>
        {run && <span className={`badge status-${run.status}`}>{run.status}</span>}
        {run && <RunStats run={run} replayEnd={replayEnd} />}
        {run && run.versions.length > 1 && (
          <select value={version ?? ""} onChange={(e) => setVersion(e.target.value ? Number(e.target.value) : null)} aria-label="Graph version">
            <option value="">graph v{run.graphVersion} (latest)</option>
            {run.versions.slice(1).map((v) => (
              <option key={v.version} value={v.version}>
                v{v.version}: {v.reason.slice(0, 60)}
              </option>
            ))}
          </select>
        )}
        {waiting.map((g) => (
          <button key={g.id} className="gate-chip" onClick={() => setSelected(g.id)}>
            {g.id} waits for you
          </button>
        ))}
      </header>

      <main className="body">
        <div className="canvas">
          {run && graph ? (
            <GraphView run={run} graph={graph} marks={marks} selected={selected} onSelect={setSelected} now={replayEnd} />
          ) : (
            loaded?.length === 0 && <EmptyState />
          )}
        </div>

        <aside className="panel">
          {run && selected && run.nodes[selected] ? (
            <NodePanel key={selected} run={run} id={selected} live={live} onSelect={setSelected} onClose={() => setSelected(undefined)} />
          ) : (
            run && (
              <section className="panel-section">
                <h2>Run {run.id}</h2>
                <p className="muted">
                  Graph <span className="mono">{run.graph.name}</span>, version {run.graphVersion}.
                </p>
                {run.reason && <p className={run.status === "failed" ? "error" : "muted"}>{run.reason}</p>}
                <h3>Events</h3>
                <ol className="events">
                  {recent.map((e) => (
                    <li key={e.seq}>
                      <button className="event" disabled={!e.node} onClick={() => e.node && setSelected(e.node)}>
                        <span className="seq">{clock(Date.parse(e.at) - Date.parse(run.startedAt))}</span>
                        <span className={`etype t-${e.type}`}>{describe(e)}</span>
                      </button>
                    </li>
                  ))}
                </ol>
              </section>
            )
          )}
        </aside>
      </main>

      <footer className="playback">
        <button
          onClick={() => {
            if (playing) return setPlaying(false);
            if (cursor === null || cursor >= events.length) setCursor(0);
            setPlaying(true);
          }}
          disabled={events.length === 0}
        >
          {playing ? "Pause" : "Replay"}
        </button>
        <Timeline events={events} count={count}>
          <input
            type="range"
            min={0}
            max={events.length}
            value={cursor ?? events.length}
            onChange={(e) => {
              setPlaying(false);
              setCursor(Number(e.target.value));
            }}
            aria-label="Event position"
          />
        </Timeline>
        <span className="pos mono">
          {cursor ?? events.length} / {events.length}
        </span>
        <button
          className={live ? "live on" : "live"}
          onClick={() => {
            setPlaying(false);
            setCursor(null);
          }}
        >
          Live
        </button>
      </footer>
    </div>
  );
}
