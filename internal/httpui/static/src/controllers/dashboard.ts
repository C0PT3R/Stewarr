import { formatBytes } from "../shared";

interface CleanupStats {
  ReclaimedBytes?: number;
  Runs?: number;
  MediaRemoved?: number;
  TorrentsRemoved?: number;
  MediaBytes?: number;
  Last30Bytes?: number;
}

interface DashboardResponse {
  stats?: CleanupStats;
  [key: string]: unknown;
}

export class DashboardController extends window.Stimulus.Controller {
  etag = "";
  onRevision!: (event: CustomEvent) => void;

  connect(): void {
    this.etag = "";
    this.onRevision = (event: CustomEvent) => {
      const kind = String(event.detail?.kind || "background");
      if (kind === "tasks") return;
      this.refresh();
    };
    document.addEventListener("stewarr:revision", this.onRevision as EventListener);
  }

  disconnect(): void {
    document.removeEventListener("stewarr:revision", this.onRevision as EventListener);
  }

  field(name: string): HTMLElement | null {
    return this.element.querySelector(`[data-dashboard-field="${name}"]`);
  }

  set(name: string, value: unknown): void {
    const element = this.field(name);
    if (element) element.textContent = String(value);
  }

  async refresh(): Promise<void> {
    const headers: Record<string, string> = {};
    if (this.etag) headers["If-None-Match"] = this.etag;
    try {
      const response = await fetch("/api/dashboard", { headers, cache: "no-store" });
      if (response.status === 304) return;
      if (!response.ok) throw new Error(`status ${response.status}`);
      this.etag = response.headers.get("ETag") || "";
      const data: DashboardResponse = await response.json();
      for (const name of ["totalMedia", "movies", "series", "libraryBytes", "totalTorrents", "current", "superseded", "orphaned", "unassociated", "obsoleteReclaimable"]) this.set(name, data[name]);
      const stats = data.stats || {};
      this.set("reclaimedBytes", formatBytes(stats.ReclaimedBytes));
      this.set("runs", stats.Runs || 0);
      this.set("mediaRemoved", stats.MediaRemoved || 0);
      this.set("torrentsRemoved", stats.TorrentsRemoved || 0);
      this.set("mediaBytes", formatBytes(stats.MediaBytes));
      this.set("last30Bytes", formatBytes(stats.Last30Bytes));
    } catch (_) {
      // The revision controller's conditional polling will try again. Existing
      // published values remain visible instead of blanking a healthy card.
    }
  }
}
