import type { GateComment } from "./gen/events";

// A draft is the notes and comments not yet sent. React state is the truth; storage is a copy.
export interface Draft {
  notes: string;
  comments: GateComment[];
}

const key = (run: string, gate: string, round: number) => `galvantula:draft:${run}:${gate}:${round}`;

export function loadDraft(run: string, gate: string, round: number): Draft {
  try {
    const d = JSON.parse(localStorage.getItem(key(run, gate, round)) ?? "null");
    if (d && typeof d.notes === "string" && Array.isArray(d.comments)) return d;
  } catch {
    // Storage is off or holds junk: start empty.
  }
  return { notes: "", comments: [] };
}

export function saveDraft(run: string, gate: string, round: number, draft: Draft): void {
  try {
    if (draft.notes === "" && draft.comments.length === 0) localStorage.removeItem(key(run, gate, round));
    else localStorage.setItem(key(run, gate, round), JSON.stringify(draft));
  } catch {
    // Storage is off or full: the draft stays in memory.
  }
}
