import { announce, dispatchRevision } from "../shared";

interface ModalRoot extends HTMLElement {
  _stewarrOpener?: HTMLElement;
}

interface StorageStatsDevice {
  representativePath: string;
  available: boolean;
  totalBytes: number;
  freeBytes: number;
  usedBytes: number;
  usagePercent: number;
  targetUsagePercent: number;
  criticalUsagePercent: number;
  otherBytes: number;
  reservedBytes: number;
  usableBytes: number;
  stewarrUsedBytes: number;
  usagePercentOfUsable: number;
}

// Mirrors cleanup.Human's formatting exactly (internal/cleanup/plan.go) so
// the live-patched summary line never visibly disagrees with the same
// number rendered server-side elsewhere on the page.
function humanBytes(bytes: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

export class ShellController extends window.Stimulus.Controller {
  filterTimer: ReturnType<typeof setTimeout> | null = null;
  modalRequest: AbortController | null = null;
  onClick!: (event: MouseEvent) => void;
  onInput!: (event: Event) => void;
  onChange!: (event: Event) => void;
  onSubmit!: (event: Event) => void;
  onRevision!: (event: CustomEvent) => void;
  onApply!: () => void;
  onAccepted!: () => void;

  connect(): void {
    this.filterTimer = null;
    this.modalRequest = null;
    this.onClick = event => this.click(event);
    this.onInput = event => this.filterInput(event);
    this.onChange = event => this.filterChange(event);
    this.onSubmit = event => this.submit(event);
    this.onRevision = event => this.revision(event.detail || {});
    this.onApply = () => this.refreshFragments(true, true);
    this.onAccepted = () => this.refreshFragments(true, true);
    document.addEventListener("click", this.onClick);
    document.addEventListener("input", this.onInput);
    document.addEventListener("change", this.onChange);
    document.addEventListener("submit", this.onSubmit);
    document.addEventListener("stewarr:revision", this.onRevision as EventListener);
    document.addEventListener("stewarr:apply-updates", this.onApply);
    document.addEventListener("stewarr:mutation-accepted", this.onAccepted);
  }

  disconnect(): void {
    document.removeEventListener("click", this.onClick);
    document.removeEventListener("input", this.onInput);
    document.removeEventListener("change", this.onChange);
    document.removeEventListener("submit", this.onSubmit);
    document.removeEventListener("stewarr:revision", this.onRevision as EventListener);
    document.removeEventListener("stewarr:apply-updates", this.onApply);
    document.removeEventListener("stewarr:mutation-accepted", this.onAccepted);
    if (this.filterTimer) clearTimeout(this.filterTimer);
    if (this.modalRequest) this.modalRequest.abort();
  }

  click(event: MouseEvent): void {
    const target = event.target as HTMLElement;
    const removal = target.closest<HTMLElement>("[data-removal-url]");
    if (removal) {
      event.preventDefault();
      this.openOverlay(removal.dataset.removalUrl!, removal);
      return;
    }
    const overlay = target.closest<HTMLElement>("[data-overlay-url]");
    if (overlay) {
      event.preventDefault();
      this.openOverlay(overlay.dataset.overlayUrl!, overlay);
      return;
    }
    const loadingCancel = target.closest("[data-modal-cancel-loading]");
    if (loadingCancel) {
      event.preventDefault();
      if (this.modalRequest) this.modalRequest.abort();
      this.closeModal();
      return;
    }
    const overlayCancel = target.closest("[data-overlay-cancel]");
    if (overlayCancel) {
      event.preventDefault();
      this.closeModal();
      return;
    }
    const clear = target.closest<HTMLAnchorElement>("[data-filter-clear]");
    if (clear) {
      event.preventDefault();
      const form = clear.closest("body")?.querySelector<HTMLFormElement>("form[data-auto-filter]");
      if (form) {
        form.reset();
        this.navigateList(clear.href);
      }
      return;
    }
    const listLink = target.closest<HTMLAnchorElement>("[data-filter-results] a[href]:not([data-list-item-link])");
    if (listLink && listLink.origin === location.origin) {
      event.preventDefault();
      this.navigateList(listLink.href);
      return;
    }
    const all = target.closest<HTMLInputElement>("#unmanagedAll");
    if (all) {
      document.querySelectorAll<HTMLInputElement>(".unmanagedPick").forEach(input => { input.checked = all.checked; });
      this.syncUnmanagedSelection();
    }
    const serviceTest = target.closest<HTMLElement>("[data-service-test]");
    if (serviceTest) {
      event.preventDefault();
      this.testServiceConnection(serviceTest);
    }
    const tmdbTest = target.closest<HTMLElement>("[data-tmdb-test]");
    if (tmdbTest) {
      event.preventDefault();
      this.testTMDBConnection(tmdbTest);
    }
    const cleanupProtect = target.closest<HTMLElement>("[data-cleanup-protect]");
    if (cleanupProtect) {
      event.preventDefault();
      this.protectCleanupAction(cleanupProtect);
    }
    const cleanupExclude = target.closest<HTMLElement>("[data-cleanup-exclude]");
    if (cleanupExclude) {
      event.preventDefault();
      this.excludeCleanupAction(cleanupExclude);
    }
  }

  filterInput(event: Event): void {
    const target = event.target as HTMLInputElement;
    const form = target.closest("form[data-auto-filter]");
    if (!form || target.type !== "search") return;
    if (this.filterTimer) clearTimeout(this.filterTimer);
    this.filterTimer = setTimeout(() => this.navigateList(this.formURL(form as HTMLFormElement)), 180);
  }

  filterChange(event: Event): void {
    const target = event.target as HTMLElement;
    if (target.matches("[data-service-type]")) {
      this.syncServiceFields(target as HTMLSelectElement);
      return;
    }
    if (target.matches(".unmanagedPick")) {
      this.syncUnmanagedSelection();
      return;
    }
    if (target.matches("[data-tmdb-toggle]")) {
      this.syncTMDBFields(target as HTMLInputElement);
      return;
    }
    if (target.matches("[data-automatic-removal-toggle]")) {
      this.syncThresholdFields(target as HTMLInputElement);
      return;
    }
    const form = target.closest<HTMLFormElement>("form[data-auto-filter]");
    if (form) this.navigateList(this.formURL(form));
  }

  // Each service type only needs a subset of credential fields (an API
  // key, or a username+password, never both) — show only the ones that
  // apply to whatever type is currently selected in the Add service form.
  syncServiceFields(select: HTMLSelectElement): void {
    const form = select.closest("form");
    if (!form) return;
    const type = select.value;
    for (const field of form.querySelectorAll<HTMLElement>("[data-service-field]")) {
      const types = (field.dataset.serviceField || "").split(/\s+/);
      field.hidden = !types.includes(type);
    }
  }

  // Hides the API key field (and Test button) when TMDB enrichment is
  // switched off, and clears the key's value so submitting the form in
  // that state actually disables it server-side rather than resubmitting
  // whatever was last saved — there is no separate on/off switch,
  // clearing the key IS how it's disabled.
  syncTMDBFields(toggle: HTMLInputElement): void {
    const form = toggle.closest("form");
    if (!form) return;
    const enabled = toggle.checked;
    for (const field of form.querySelectorAll<HTMLElement>("[data-tmdb-field]")) field.hidden = !enabled;
    if (!enabled) {
      const key = form.querySelector<HTMLInputElement>("#settings-tmdb-key");
      if (key) key.value = "";
    }
  }

  // Hides the Target/Critical fields when automatic removal is disabled for
  // this device — they have no effect once nothing evaluates them, so
  // showing them would suggest a threshold is in force when none is.
  syncThresholdFields(toggle: HTMLInputElement): void {
    const form = toggle.closest("form");
    if (!form) return;
    const enabled = toggle.checked;
    for (const field of form.querySelectorAll<HTMLElement>("[data-threshold-field]")) field.hidden = !enabled;
  }

  async testTMDBConnection(button: HTMLElement): Promise<void> {
    const form = button.closest("form");
    if (!form) return;
    const resultTarget = form.querySelector<HTMLElement>("[data-tmdb-test-result]");
    const setResult = (text: string, className: string) => {
      if (!resultTarget) return;
      resultTarget.textContent = text;
      resultTarget.className = className;
    };
    (button as HTMLButtonElement).disabled = true;
    const originalLabel = button.textContent;
    button.textContent = "Testing…";
    setResult("", "");
    try {
      const response = await fetch("/settings/tmdb/test", { method: "POST", body: new FormData(form) });
      if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
      setResult("Connection verified.", "positive");
    } catch (error) {
      setResult(error instanceof Error ? error.message : String(error), "bad");
    } finally {
      (button as HTMLButtonElement).disabled = false;
      button.textContent = originalLabel;
    }
  }

  async submit(event: Event): Promise<void> {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    if (form.matches("[data-removal-launch]")) {
      event.preventDefault();
      const url = new URL(form.action, location.href);
      for (const [name, value] of new FormData(form)) url.searchParams.append(name, value as string);
      this.openOverlay(url.href, form.querySelector("button[type=submit]") as HTMLElement);
      return;
    }
    // A service overlay's final save opens step 3 (the setup-progress view)
    // instead of just closing, since saving always triggers a potentially
    // long first inventory+file scan the user otherwise gets no feedback
    // about at all.
    const thenOverlayURL = form.dataset.backgroundSubmitThenOverlay;
    if (form.matches("[data-background-submit], [data-background-submit-then-overlay]")) {
      event.preventDefault();
      const button = form.querySelector<HTMLButtonElement>("button[type=submit]");
      if (button) button.disabled = true;
      const errorTarget = form.querySelector<HTMLElement>("[data-modal-error]");
      if (errorTarget) errorTarget.hidden = true;
      try {
        const response = await fetch(form.action, { method: form.method || "POST", body: new FormData(form) });
        if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
        if (thenOverlayURL) {
          this.openOverlay(thenOverlayURL, button || form);
        } else {
          const insideModal = form.closest("#modal-root");
          if (insideModal) {
            insideModal.replaceChildren();
            document.body.classList.remove("modal-open");
          }
        }
        dispatchRevision({ kind: "operation" });
      } catch (error) {
        if (errorTarget) {
          errorTarget.textContent = (error as Error).message;
          errorTarget.hidden = false;
        } else {
          announce(`Action failed: ${(error as Error).message}`);
        }
      } finally {
        if (button) button.disabled = false;
      }
    }
  }

  // Step 1 of the service setup overlay: a live connection check with
  // nothing saved yet. Only on success does the real "Add service" submit
  // appear — storage roots are always discovered by the service's own
  // adapter after saving, never entered by hand, so there is no step 2
  // field to reveal here.
  async testServiceConnection(button: HTMLElement): Promise<void> {
    const form = button.closest("form");
    if (!form) return;
    const hint = form.closest(".modal-dialog")?.querySelector<HTMLElement>("[data-service-step-hint]");
    const errorTarget = form.querySelector<HTMLElement>("[data-modal-error]");
    if (errorTarget) errorTarget.hidden = true;
    (button as HTMLButtonElement).disabled = true;
    const originalLabel = button.textContent;
    button.textContent = "Testing…";
    try {
      const response = await fetch("/services/test", { method: "POST", body: new FormData(form) });
      if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
      const submitButton = form.querySelector<HTMLElement>("[data-service-submit]");
      if (submitButton) submitButton.hidden = false;
      button.hidden = true;
      if (hint) hint.textContent = "Connection verified. Save to finish.";
    } catch (error) {
      if (errorTarget) {
        errorTarget.textContent = (error as Error).message;
        errorTarget.hidden = false;
      } else {
        announce(`Connection test failed: ${(error as Error).message}`);
      }
    } finally {
      (button as HTMLButtonElement).disabled = false;
      button.textContent = originalLabel;
    }
  }

  // The cleanup-plan form's own currently-excluded keys (rendered back as
  // hidden fields by the server every time the plan is recomputed — see
  // cleanupPlanData.Excluded) — read fresh each time rather than tracked
  // separately in JS, so there's exactly one source of truth for "what's
  // currently excluded," never at risk of drifting from what the modal
  // actually displays.
  currentExcludedKeys(button: HTMLElement): string[] {
    const form = button.closest("form");
    if (!form) return [];
    return [...form.querySelectorAll<HTMLInputElement>('input[name="excluded"]')].map(input => input.value);
  }

  // Reopens the cleanup-plan overlay with key added to the excluded set —
  // the server recomputes the plan leaving that action out of selection
  // entirely, so a different candidate immediately fills the gap rather
  // than the device's cleanup target going unmet (see cleanup.Build's
  // excluded parameter). Reused by both Skip (no other effect) and
  // Protect (after the permanent tag write actually succeeds).
  reopenCleanupPlanExcluding(button: HTMLElement, key: string): void {
    const path = button.dataset.path;
    if (!path) return;
    const url = new URL("/storage/cleanup-plan", location.origin);
    url.searchParams.set("path", path);
    for (const existing of this.currentExcludedKeys(button)) url.searchParams.append("excluded", existing);
    url.searchParams.append("excluded", key);
    this.openOverlay(url.pathname + url.search, button);
  }

  // Skip is transient and purely a recompute request — nothing is
  // persisted, so unlike Protect there's no network round trip before
  // reopening with the new exclusion in place.
  excludeCleanupAction(button: HTMLElement): void {
    const key = button.dataset.key;
    if (!key) return;
    this.reopenCleanupPlanExcluding(button, key);
  }

  // Independent of the cleanup-plan form's own Clean submission — a plain
  // button (not a nested form), read via its own data-path/data-key
  // attributes, so protecting one row works immediately while still
  // reviewing the rest of the list instead of submitting the whole batch.
  // Sends the form's current excluded keys along too, so the server finds
  // this exact row in the same already-filtered plan the user is looking
  // at (a fresh, entirely unexcluded rebuild could have chosen a
  // different replacement candidate instead of this one).
  async protectCleanupAction(button: HTMLElement): Promise<void> {
    const path = button.dataset.path;
    const key = button.dataset.key;
    if (!path || !key) return;
    (button as HTMLButtonElement).disabled = true;
    try {
      const body = new URLSearchParams({ path, key });
      for (const existing of this.currentExcludedKeys(button)) body.append("excluded", existing);
      const response = await fetch("/storage/cleanup-plan/protect", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body,
      });
      if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
      this.reopenCleanupPlanExcluding(button, key);
    } catch (error) {
      announce(`Protect failed: ${(error as Error).message}`);
      (button as HTMLButtonElement).disabled = false;
    }
  }

  // One visible .unmanagedPick checkbox per physical file, regardless of
  // how many hardlinked paths it has — selecting it mirrors onto every
  // hidden .unmanagedGroupPath (one per path, all name="path") sharing its
  // data-unmanaged-group. Partial selection (only some of a file's
  // hardlinks) was tried and rejected: it silently reclaims 0 bytes, since
  // the remaining link keeps the data alive, with no visible reason why.
  syncUnmanagedSelection(): void {
    const picks = [...document.querySelectorAll<HTMLInputElement>(".unmanagedPick")];
    for (const pick of picks) {
      document.querySelectorAll<HTMLInputElement>(`.unmanagedGroupPath[data-unmanaged-group="${CSS.escape(pick.dataset.unmanagedGroup!)}"]`).forEach(input => { input.checked = pick.checked; });
    }
    const selected = picks.filter(input => input.checked).length;
    const all = document.getElementById("unmanagedAll") as HTMLInputElement | null;
    if (all) {
      all.checked = picks.length > 0 && selected === picks.length;
      all.indeterminate = selected > 0 && selected < picks.length;
    }
    const button = document.getElementById("unmanagedRemoveButton") as HTMLButtonElement | null;
    if (button) button.disabled = selected === 0;
  }

  formURL(form: HTMLFormElement): string {
    const url = new URL(form.action, location.href);
    const values = new FormData(form);
    for (const [name, value] of values) url.searchParams.append(name, value as string);
    return url.href;
  }

  navigateList(url: string): void {
    const fragment = document.querySelector<HTMLElement>("[data-filter-results]");
    if (!fragment || !fragment.id) return;
    history.replaceState({}, "", url);
    this.swapFragment(fragment, url, true);
    const updates = document.getElementById("updates-available");
    if (updates) updates.hidden = true;
  }

  // userTriggered marks the swap with [data-user-triggered-swap] so the
  // CSS loading dip (app.css) applies — passive/background swaps (revision
  // pushes, polling) are the normal case and stay silent by default; only a
  // swap the user directly asked for opts in to the visual feedback.
  swapFragment(fragment: HTMLElement, url: string = location.href, userTriggered: boolean = false): Promise<void> {
    if (userTriggered) fragment.setAttribute("data-user-triggered-swap", "");
    return window.htmx.ajax("GET", url, {
      source: fragment,
      target: `#${CSS.escape(fragment.id)}`,
      select: `#${CSS.escape(fragment.id)}`,
      swap: "outerHTML"
    });
  }

  revision(detail: Record<string, unknown>): void {
    this.refreshWizardProgress();
    const kind = String(detail.kind || "background");
    const urgent = /mutation|operation|removal|failure/.test(kind);
    // A raw disk-byte tick (watchStorageChanges polls every 5s) is routine
    // background noise almost everywhere — it never means a filtered list's
    // results changed. On the Storage page itself it specifically must never
    // force the (comparatively expensive) removal plan to recompute just
    // because free space ticked; only the cheap byte totals update this
    // often, via a direct fetch/patch rather than a fragment swap.
    if (kind === "storage" && document.querySelector("[data-storage-summary]")) {
      this.refreshStorageStats();
      return;
    }
    const stable = document.querySelector('[data-live-policy="stable-list"]');
    if (stable && (kind === "tasks" || kind === "startup" || kind === "storage")) return;
    if (stable && !urgent) {
      const updates = document.getElementById("updates-available");
      if (updates) updates.hidden = false;
      return;
    }
    this.refreshFragments(urgent, false);
  }

  // Step 3 of the service setup overlay has no push mechanism of its own —
  // it piggybacks on the SSE revision stream that already fires on every
  // task-manager change (including this workflow's own step advances), and
  // just re-fetches the current status on each tick, in place, without a
  // full fragment re-render.
  async refreshWizardProgress(): Promise<void> {
    const root = document.querySelector("[data-wizard-progress]");
    if (!root) return;
    try {
      const response = await fetch("/services/consistency-status");
      if (!response.ok) return;
      const status = await response.json();
      const label = root.querySelector<HTMLElement>("[data-wizard-progress-label]");
      const fill = root.querySelector<HTMLElement>(".wizard-progress-bar-fill");
      if (label) {
        label.textContent = status.state === "succeeded" || status.state === "attention"
          ? "Done."
          : `Step ${status.currentStep + 1} of ${status.totalSteps}: ${status.stepLabel || "Working…"}`;
      }
      if (fill) fill.classList.toggle("done", status.state === "succeeded" || status.state === "attention");
    } catch (_) {
      // A failed status poll leaves the last-known label in place; the next
      // revision tick tries again.
    }
  }

  // The Storage page's "used of total" line updates straight from the cheap
  // /storage/stats endpoint (raw statfs bytes only — no removal plan) rather
  // than through the fragment-swap machinery, so a byte-level tick every few
  // seconds never re-renders the bar/legend/plan or touches the threshold
  // form at all.
  async refreshStorageStats(): Promise<void> {
    try {
      const response = await fetch("/storage/stats", { cache: "no-store" });
      if (!response.ok) return;
      const devices: StorageStatsDevice[] = await response.json();
      for (const device of devices) {
        if (!device.available) continue;
        const summary = document.querySelector<HTMLElement>(
          `[data-storage-summary][data-representative-path="${CSS.escape(device.representativePath)}"]`
        );
        if (!summary) continue;
        const unexplainedOther = device.otherBytes > device.reservedBytes ? device.otherBytes - device.reservedBytes : 0;
        const reserved = device.reservedBytes > 0 ? ` · ${humanBytes(device.reservedBytes)} reserved by the filesystem` : "";
        const elsewhere = unexplainedOther > 0 ? ` · ${humanBytes(unexplainedOther)} used elsewhere on this ${humanBytes(device.totalBytes)} disk` : "";
        summary.textContent = `${humanBytes(device.stewarrUsedBytes)} used of ${humanBytes(device.usableBytes)} usable (${device.usagePercentOfUsable.toFixed(1)}%, target ${device.targetUsagePercent.toFixed(1)}%)${reserved}${elsewhere}`;
      }
    } catch (_) {
      // The next 5s tick tries again; the last-known text stays in place.
    }
  }

  refreshFragments(force: boolean, userTriggered: boolean = false): void {
    const fragments = [...document.querySelectorAll<HTMLElement>("[data-reactive-fragment][id]")];
    for (const fragment of fragments) {
      if (fragment.dataset.livePolicy === "stable-list" && !force) continue;
      this.swapFragment(fragment, location.href, userTriggered);
    }
    const updates = document.getElementById("updates-available");
    if (updates && force) updates.hidden = true;
  }

  async openOverlay(url: string, opener: HTMLElement): Promise<void> {
    if (this.modalRequest) this.modalRequest.abort();
    this.modalRequest = new AbortController();
    const root = document.getElementById("modal-root") as ModalRoot | null;
    if (!root) return;
    root.innerHTML = '<div class="modal-overlay"><main class="modal-dialog preparing" role="dialog" aria-modal="true"><p class="muted">Loading…</p><div class="actions"><button type="button" data-modal-cancel-loading>Cancel</button></div></main></div>';
    root.dataset.openerId = opener.id || "";
    root._stewarrOpener = opener;
    document.body.classList.add("modal-open");
    try {
      const response = await fetch(url, { signal: this.modalRequest.signal, headers: { "X-Stewarr-Overlay": "1" } });
      const content = await response.text();
      if (!response.ok) throw new Error(content.trim() || `status ${response.status}`);
      root.innerHTML = content;
      window.htmx.process(root);
      this.refreshWizardProgress();
      // A category-narrowed Type select (e.g. only qBittorrent, opened from
      // the Torrents page) may default to a type whose credential fields
      // don't match the template's static hidden attributes (written
      // assuming Radarr is first/default) — sync once against whatever the
      // select actually opened with, not just on a later change event.
      const serviceType = root.querySelector<HTMLSelectElement>("[data-service-type]");
      if (serviceType) this.syncServiceFields(serviceType);
    } catch (error) {
      if ((error as Error).name === "AbortError") return;
      root.innerHTML = `<div class="modal-overlay"><main class="modal-dialog" role="dialog" aria-modal="true"><p class="bad"></p><div class="actions"><button type="button" data-modal-cancel-loading>Close</button></div></main></div>`;
      root.querySelector(".bad")!.textContent = `Unavailable: ${(error as Error).message}`;
    } finally {
      this.modalRequest = null;
    }
  }

  closeModal(): void {
    const root = document.getElementById("modal-root") as ModalRoot | null;
    if (!root) return;
    const opener = root._stewarrOpener;
    root.replaceChildren();
    document.body.classList.remove("modal-open");
    if (opener && document.contains(opener)) opener.focus();
  }
}
