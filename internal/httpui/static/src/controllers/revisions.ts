import { dispatchRevision } from "../shared";

interface RevisionInvalidation {
  kind?: string;
}

interface StatusNotice {
  id: string;
  label?: string;
  message?: string;
  status?: string;
}

interface StatusResponse {
  kind?: string;
  pendingOperations?: number;
  notices?: StatusNotice[];
  [key: string]: unknown;
}

const maxReconnectAttempts = 5;

export class RevisionsController extends window.Stimulus.Controller {
  etag = "";
  pollTimer: ReturnType<typeof setInterval> | null = null;
  reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  events: EventSource | null = null;
  // The server voluntarily ends and lets the client reconnect every few
  // minutes (a connection can go silently dead through a network path with
  // neither side ever seeing an error, so relying on failure detection
  // alone isn't enough) — EventSource has no way to tell "the server closed
  // this on purpose" apart from "the network died", both surface as the
  // same error event. Counting consecutive failures, and resetting the
  // count on every successful open, is what keeps a routine server-side
  // rotation from permanently downgrading every page load to polling after
  // a few minutes.
  reconnectAttempts = 0;
  onVisible!: () => void;
  // A newly opened EventSource always immediately replays whatever revision
  // is already current, so the connection's very first "revision" message
  // merely restates what this page's HTML was just rendered with — not
  // something that changed since. Without this flag that replay looked
  // identical to a live change, so "Updates available" would appear on
  // virtually every page load/refresh. Only messages after the first one
  // reflect a change that actually happened after the page loaded.
  syncedInitialRevision = false;
  onPageHide!: () => void;

  connect(): void {
    this.etag = "";
    this.pollTimer = null;
    this.reconnectTimer = null;
    this.reconnectAttempts = 0;
    this.syncedInitialRevision = false;
    this.onVisible = () => {
      if (!document.hidden) this.refreshStatus({ kind: "visibility" });
    };
    document.addEventListener("visibilitychange", this.onVisible);
    // A full-page navigation tears down this whole document (DOM, JS heap,
    // Stimulus's own MutationObserver included) as part of the browser's
    // navigation algorithm — disconnect() below is not guaranteed to run,
    // or to run early enough, ahead of the new page's own connections
    // opening. pagehide fires synchronously and reliably before that
    // teardown, so it's the only place we can be sure the old page's
    // EventSource is actually closed before it competes with the new
    // page for one of the browser's ~6 connections-per-origin.
    this.onPageHide = () => {
      if (this.events) {
        this.events.close();
        this.events = null;
      }
    };
    window.addEventListener("pagehide", this.onPageHide);
    this.connectEvents();
  }

  disconnect(): void {
    document.removeEventListener("visibilitychange", this.onVisible);
    window.removeEventListener("pagehide", this.onPageHide);
    if (this.events) this.events.close();
    if (this.pollTimer) clearInterval(this.pollTimer);
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
  }

  connectEvents(): void {
    if (!("EventSource" in window)) {
      this.startPolling();
      return;
    }
    // A dropped EventSource that isn't explicitly closed keeps retrying to
    // reconnect in the background forever, per spec, even after this
    // controller has moved on to its own retry/poll logic below. Each
    // silent retry attempt still consumes one of the browser's ~6
    // connections-per-origin — left unclosed long enough (one tab, open for
    // hours, hitting the odd network blip), those zombie reconnect loops
    // alone can exhaust the pool and stall every other request to this
    // origin indefinitely. Always close the previous object ourselves
    // before creating a new one.
    if (this.events) this.events.close();
    this.events = new EventSource("/ui/events");
    this.events.addEventListener("revision", event => {
      try {
        this.refreshStatus(JSON.parse((event as MessageEvent).data));
      } catch (_) {
        this.refreshStatus({ kind: "background" });
      }
    });
    this.events.onopen = () => {
      this.reconnectAttempts = 0;
      if (this.pollTimer) {
        clearInterval(this.pollTimer);
        this.pollTimer = null;
      }
    };
    this.events.onerror = () => {
      if (this.events) {
        this.events.close();
        this.events = null;
      }
      // EventSource gives no way to tell "the server ended this on purpose"
      // (a routine periodic rotation) apart from "the network actually
      // failed" — both fire this same error event. Treating every single
      // error as a permanent downgrade to polling would mean every page
      // load loses live updates within minutes, as soon as the server's
      // first scheduled rotation happens. Retry a handful of times first
      // (with backoff, so a genuine outage doesn't hammer the server) and
      // only fall back to polling if reconnecting keeps failing.
      this.reconnectAttempts++;
      if (this.reconnectAttempts > maxReconnectAttempts) {
        this.startPolling();
        return;
      }
      const delay = Math.min(500 * 2 ** (this.reconnectAttempts - 1), 8000);
      this.reconnectTimer = setTimeout(() => this.connectEvents(), delay);
    };
  }

  startPolling(): void {
    if (this.pollTimer) return;
    this.pollTimer = setInterval(() => {
      if (!document.hidden) this.refreshStatus({ kind: "poll" });
    }, 5000);
  }

  async refreshStatus(invalidation: RevisionInvalidation): Promise<void> {
    const headers: Record<string, string> = {};
    if (this.etag) headers["If-None-Match"] = this.etag;
    try {
      const response = await fetch("/ui/status", { headers, cache: "no-store" });
      if (response.status === 304) return;
      if (!response.ok) throw new Error(`status ${response.status}`);
      this.etag = response.headers.get("ETag") || "";
      const status: StatusResponse = await response.json();
      this.renderStatus(status);
      const isPushInvalidation = invalidation.kind !== "poll" && invalidation.kind !== "visibility";
      if (isPushInvalidation && !this.syncedInitialRevision) {
        this.syncedInitialRevision = true;
        return;
      }
      const sourceKind = invalidation.kind === "poll" || invalidation.kind === "visibility" ? status.kind : invalidation.kind;
      dispatchRevision({ ...status, kind: sourceKind || status.kind });
    } catch (_) {
      this.startPolling();
    }
  }

  renderStatus(status: StatusResponse): void {
    const indicator = document.getElementById("operation-indicator");
    if (indicator) {
      const count = Number(status.pendingOperations) || 0;
      indicator.hidden = count === 0;
      indicator.textContent = count === 1 ? "1 operation in progress" : `${count} operations in progress`;
    }
    const notices = document.getElementById("persistent-notices");
    if (notices) {
      notices.replaceChildren(...(status.notices || []).map(notice => {
        const item = document.createElement("a");
        item.className = "persistent-notice bad";
        item.href = `/history#operation-${notice.id}`;
        item.textContent = `${notice.label || "Removal"}: ${notice.message || notice.status}`;
        return item;
      }));
    }
  }
}
