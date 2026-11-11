import type { ReactNode } from "react";

export type Block =
  | { kind: "heading"; line: number; endLine: number; level: number; text: string }
  | { kind: "para"; line: number; endLine: number; text: string }
  | { kind: "item"; line: number; endLine: number; depth: number; marker: string; text: string }
  | { kind: "code"; line: number; endLine: number; text: string } // one line of a fenced block
  | { kind: "row"; line: number; endLine: number; head: boolean; cells: string[] }
  | { kind: "quote"; line: number; endLine: number; text: string }
  | { kind: "rule"; line: number; endLine: number };

// contractLines is the line rule: line N is element N-1, after one final \n is removed.
export const contractLines = (markdown: string): string[] => (markdown === "" ? [] : markdown.replace(/\n$/, "").split("\n"));

const FENCE = /^\s*(```|~~~)/;
const HEADING = /^(#{1,6}) +(.*?)\s*$/;
const RULE = /^ {0,3}([-*_])( *\1){2,} *$/;
const ITEM = /^(\s*)([-*+]|\d+[.)]) +(.*)$/;
const QUOTE = /^\s*> ?(.*)$/;
const SEPARATOR = /^\s*\|?[\s:|-]*-[\s:|-]*$/;

export function parseBlocks(lines: string[]): Block[] {
  const out: Block[] = [];
  let open: Extract<Block, { kind: "para" | "item" }> | undefined; // a block the next line may continue
  let fenced = false;
  lines.forEach((raw, i) => {
    const n = i + 1;
    if (fenced || FENCE.test(raw)) {
      if (FENCE.test(raw)) fenced = !fenced;
      else out.push({ kind: "code", line: n, endLine: n, text: raw });
      open = undefined;
      return;
    }
    const prev = open;
    open = undefined;
    const text = raw.trim();
    let m: RegExpMatchArray | null;
    if (text === "") return;
    if ((m = HEADING.exec(text))) out.push({ kind: "heading", line: n, endLine: n, level: m[1].length, text: m[2] });
    else if (RULE.test(raw)) out.push({ kind: "rule", line: n, endLine: n });
    else if (text.startsWith("|")) {
      if (SEPARATOR.test(text)) {
        const last = out[out.length - 1];
        if (last?.kind === "row") last.head = true;
      } else out.push({ kind: "row", line: n, endLine: n, head: false, cells: text.replace(/^\||\|$/g, "").split("|").map((c) => c.trim()) });
    } else if ((m = QUOTE.exec(raw))) out.push({ kind: "quote", line: n, endLine: n, text: m[1] });
    else if ((m = ITEM.exec(raw))) out.push((open = { kind: "item", line: n, endLine: n, depth: Math.floor(m[1].length / 2), marker: m[2], text: m[3] }));
    else if (prev) {
      prev.text += " " + text;
      prev.endLine = n;
      open = prev;
    } else out.push((open = { kind: "para", line: n, endLine: n, text }));
  });
  return out;
}

const MARKS = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*\s][^*]*\*|(?<!\w)_(?=\S)[^_]+_(?!\w))/;
const LINK = /(?<!!)\[([^\]]*)\]\(([^)]*)\)/g;

// Inline builds React elements only, so text from an agent never becomes HTML.
export function Inline({ text }: { text: string }) {
  const parts: ReactNode[] = text.split(MARKS).map((s, i) => {
    if (i % 2 === 0) return s.replace(LINK, "$1 ($2)");
    if (s.startsWith("`")) return <code key={i}>{s.slice(1, -1)}</code>;
    if (s.startsWith("**")) return <strong key={i}>{s.slice(2, -2)}</strong>;
    return <em key={i}>{s.slice(1, -1)}</em>;
  });
  return <>{parts}</>;
}

if (import.meta.env.DEV) {
  const got = parseBlocks(["# T", "", "para one", "runs on", "- a", "  - b", "2. c", "```go", "x := 1", "", "```", "| h |", "|---|", "| v |", "> q", "---", "<b>raw</b>"]);
  const kinds = got.map((b) => b.kind).join(" ");
  console.assert(kinds === "heading para item item item code code row row quote rule para", "parseBlocks kinds", kinds);
  console.assert(got[1].endLine === 4 && got[3].kind === "item" && got[3].depth === 1, "parseBlocks lines and depth");
  console.assert(got[5].line === 9 && got[6].line === 10, "parseBlocks code line numbers");
  console.assert(got[7].kind === "row" && got[7].head && got[7].line === 12 && got[8].line === 14, "parseBlocks table");
}
