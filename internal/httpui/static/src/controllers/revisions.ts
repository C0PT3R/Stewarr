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

export class RevisionsController extends window.Stimulus.Controller {
  etag = "";
  pollTimer: ReturnType<typeof setInterval> | null = null;
  events: EventSource | null = null;
  onVisible!: () => void;
  // A newly opened EventSource always immediately replays whatever revision
  // is already current, so the connection's very first "revision" message
  // merely restates what this page's HTML was just rendered with — not
  // something that changed since. Without this flag that replay looked
  // identical to a live change, so "Updates available" would appear on
  // virtually every page load/refresh. Only messages after the first one
  // reflect a change that actually happened after the page loaded.
  syncedInitialRevision = false;

  connect(): void {
    this.etag = "";
    this.pollTimer = null;
    this.syncedInitialRevision = false;
    this.onVisible = () => {
      if (!document.hidden) this.refreshStatus({ kind: "visibility" });
    };
    document.addEventListener("visibilitychange", this.onVisible);
    this.connectEvents();
  }

  disconnect(): void {
    document.removeEventListener("visibilitychange", this.onVisible);
    if (this.events) this.events.close();
    if (this.pollTimer) clearInterval(this.pollTimer);
  }

  connectEvents(): void {
    if (!("EventSource" in window)) {
      this.startPolling();
      return;
    }
    this.events = new EventSource("/ui/events");
    this.events.addEventListener("revision", event => {
      try {
        this.refreshStatus(JSON.parse((event as MessageEvent).data));
      } catch (_) {
        this.refreshStatus({ kind: "background" });
      }
    });
    this.events.onopen = () => {
      if (this.pollTimer) {
        clearInterval(this.pollTimer);
        this.pollTimer = null;
      }
    };
    this.events.onerror = () => this.startPolling();
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
