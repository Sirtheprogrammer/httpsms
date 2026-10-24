import { useEffect, useRef, useState } from 'react';

type Handler = (data: any) => void;

// One shared WebSocket to /ws/dashboard; the server only sends events the user may see.
class LiveSocket {
  private ws: WebSocket | null = null;
  private handlers = new Map<string, Set<Handler>>();
  private statusHandlers = new Set<(online: boolean) => void>();
  private attempts = 0;
  private timer: number | undefined;
  private stopped = true;
  online = false;

  start() {
    if (!this.stopped) return;
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    window.clearTimeout(this.timer);
    this.ws?.close();
    this.ws = null;
  }

  private connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/ws/dashboard`);
    this.ws = ws;
    ws.onopen = () => {
      this.attempts = 0;
      this.setOnline(true);
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        this.handlers.get(msg.event)?.forEach((h) => h(msg.data));
        this.handlers.get('*')?.forEach((h) => h(msg));
      } catch {
        /* ignore malformed frames */
      }
    };
    ws.onclose = () => {
      this.setOnline(false);
      if (this.stopped) return;
      const delay = Math.min(1000 * 1.6 ** this.attempts++, 15000);
      this.timer = window.setTimeout(() => this.connect(), delay);
    };
  }

  private setOnline(v: boolean) {
    this.online = v;
    this.statusHandlers.forEach((h) => h(v));
  }

  on(event: string, h: Handler) {
    if (!this.handlers.has(event)) this.handlers.set(event, new Set());
    this.handlers.get(event)!.add(h);
    return () => this.handlers.get(event)!.delete(h);
  }

  onStatus(h: (online: boolean) => void) {
    this.statusHandlers.add(h);
    return () => this.statusHandlers.delete(h);
  }
}

export const live = new LiveSocket();

/** Subscribe to one or more live events while the component is mounted. */
export function useLive(events: string | string[], handler: Handler) {
  const ref = useRef(handler);
  ref.current = handler;
  const key = Array.isArray(events) ? events.join(',') : events;
  useEffect(() => {
    const offs = key.split(',').map((e) => live.on(e, (d) => ref.current(d)));
    return () => offs.forEach((off) => off());
  }, [key]);
}

export function useLiveStatus() {
  const [online, setOnline] = useState(live.online);
  useEffect(() => {
    const off = live.onStatus(setOnline);
    return () => {
      off();
    };
  }, []);
  return online;
}
