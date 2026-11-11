import { useEffect, useState } from "react";

// useNow returns the current time, and again each second while active.
export function useNow(active: boolean): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

// clock formats a duration in milliseconds as "0:42", "12:05" or "1:02:03".
export function clock(ms: number): string {
  const s = Math.floor(Math.max(ms, 0) / 1000) || 0;
  const two = (n: number) => String(n).padStart(2, "0");
  const h = Math.floor(s / 3600);
  return h ? `${h}:${two(Math.floor(s / 60) % 60)}:${two(s % 60)}` : `${Math.floor(s / 60)}:${two(s % 60)}`;
}
