import { useEffect, useMemo, useRef, useState } from "react";
import type { FactoryEvent, RunSummary } from "./gen/events";
import { GraphView } from "./GraphView";
import { NodePanel } from "./NodePanel";
import { apply, describe, fold, type RunView } from "./state";

const playStepMs = 350;

function useRuns(): RunSummary[] {
  const [runs, setRuns] = useState<RunSummary[]>([]);
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
  const runs = useRuns();
  const [chosen, setChosen] = useState(() => location.hash.slice(1));
  const runId = chosen || runs[0]?.id;
  const { events, count, live: liveRun } = useEvents(runId);
  const [cursor, setCursor] = useState<number | null>(null); // null means live
  const [playing, setPlaying] = useState(false);
  const [selected, setSelected] = useState<string>();

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
  }

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
        {waiting.map((g) => (
          <button key={g.id} className="gate-chip" onClick={() => setSelected(g.id)}>
            {g.id} waits for you
          </button>
        ))}
      </header>

      <main className="body">
        <div className="canvas">
          {run ? (
            <GraphView run={run} selected={selected} onSelect={setSelected} />
          ) : (
            <div className="empty">
              <p>No run to show.</p>
              <p className="muted mono">factory start -fake fakes/factory.yaml graphs/factory.yaml</p>
            </div>
          )}
        </div>

        <aside className="panel">
          {run && selected && run.nodes[selected] ? (
            <NodePanel key={selected} run={run} id={selected} live={live} onClose={() => setSelected(undefined)} />
          ) : (
            run && (
              <section className="panel-section">
                <h2>Run {run.id}</h2>
                <p className="muted">
                  Graph <span className="mono">{run.graph.name}</span>, version {run.graphVersion}, {run.graph.nodes.length} nodes.
                  Spent ${run.costUSD.toFixed(2)}.
                </p>
                {run.reason && <p className={run.status === "failed" ? "error" : "muted"}>{run.reason}</p>}
                <h3>Events</h3>
                <ol className="events">
                  {recent.map((e) => (
                    <li key={e.seq}>
                      <button className="event" disabled={!e.node} onClick={() => e.node && setSelected(e.node)}>
                        <span className="seq">{e.seq}</span>
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
