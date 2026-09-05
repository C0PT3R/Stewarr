import { announce, dispatchRevision } from "../shared";

interface RemovalModalRoot extends HTMLElement {
  _connarrOpener?: HTMLElement;
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
    this.onApply = () => this.refreshFragments(true);
    this.onAccepted = () => this.refreshFragments(true);
    document.addEventListener("click", this.onClick);
    document.addEventListener("input", this.onInput);
    document.addEventListener("change", this.onChange);
    document.addEventListener("submit", this.onSubmit);
    document.addEventListener("connarr:revision", this.onRevision as EventListener);
    document.addEventListener("connarr:apply-updates", this.onApply);
    document.addEventListener("connarr:mutation-accepted", this.onAccepted);
  }

  disconnect(): void {
    document.removeEventListener("click", this.onClick);
    document.removeEventListener("input", this.onInput);
    document.removeEventListener("change", this.onChange);
    document.removeEventListener("submit", this.onSubmit);
    document.removeEventListener("connarr:revision", this.onRevision as EventListener);
    document.removeEventListener("connarr:apply-updates", this.onApply);
    document.removeEventListener("connarr:mutation-accepted", this.onAccepted);
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
    const scan = target.closest<HTMLElement>("[data-unmanaged-scan]");
    if (scan) {
      event.preventDefault();
      this.scanUnmanaged(scan);
      return;
    }
    const all = target.closest<HTMLInputElement>("#unmanagedAll");
    if (all) {
      document.querySelectorAll<HTMLInputElement>(".unmanagedPick").forEach(input => { input.checked = all.checked; });
      this.syncUnmanagedSelection();
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
    if (target.matches("[data-integration-type]")) {
      this.syncIntegrationFields(target as HTMLSelectElement);
      return;
    }
    if (target.matches(".unmanagedPick")) {
      this.syncUnmanagedSelection();
      return;
    }
    const form = target.closest<HTMLFormElement>("form[data-auto-filter]");
    if (form) this.navigateList(this.formURL(form));
  }

  // Each integration type only needs a subset of credential fields (an API
  // key, or a username+password, never both) — show only the ones that
  // apply to whatever type is currently selected in the Add integration form.
  syncIntegrationFields(select: HTMLSelectElement): void {
    const form = select.closest("form");
    if (!form) return;
    const type = select.value;
    for (const field of form.querySelectorAll<HTMLElement>("[data-integration-field]")) {
      const types = (field.dataset.integrationField || "").split(/\s+/);
      field.hidden = !types.includes(type);
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
    if (form.matches("[data-background-submit]")) {
      event.preventDefault();
      const button = form.querySelector<HTMLButtonElement>("button[type=submit]");
      if (button) button.disabled = true;
      const errorTarget = form.querySelector<HTMLElement>("[data-modal-error]");
      if (errorTarget) errorTarget.hidden = true;
      try {
        const response = await fetch(form.action, { method: form.method || "POST", body: new FormData(form) });
        if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
        const insideModal = form.closest("#removal-modal");
        if (insideModal) {
          insideModal.replaceChildren();
          document.body.classList.remove("modal-open");
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

  syncUnmanagedSelection(): void {
    const picks = [...document.querySelectorAll<HTMLInputElement>(".unmanagedPick")];
    for (const pick of picks) {
      document.querySelectorAll<HTMLInputElement>(`.unmanagedGroupPath[data-unmanaged-group="${CSS.escape(pick.dataset.unmanagedGroup!)}"]`).forEach(input => { input.disabled = !pick.checked; });
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

  async scanUnmanaged(button: HTMLElement): Promise<void> {
    const old = button.textContent;
    (button as HTMLButtonElement).disabled = true;
    button.textContent = "Scanning…";
    try {
      const response = await fetch("/downloads/unmanaged/scan", { method: "POST", headers: { "X-Connarr-Scan": "1" } });
      const result = await response.json();
      if (!response.ok || !result.ok) throw new Error(result.error || `status ${response.status}`);
      this.refreshFragments(true);
      announce("Unmanaged file scan completed.");
    } catch (error) {
      announce(`Unmanaged scan failed: ${(error as Error).message}`);
    } finally {
      (button as HTMLButtonElement).disabled = false;
      button.textContent = old;
    }
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
    this.swapFragment(fragment, url);
    const updates = document.getElementById("updates-available");
    if (updates) updates.hidden = true;
  }

  swapFragment(fragment: HTMLElement, url: string = location.href): Promise<void> {
    return window.htmx.ajax("GET", url, {
      source: fragment,
      target: `#${CSS.escape(fragment.id)}`,
      select: `#${CSS.escape(fragment.id)}`,
      swap: "outerHTML"
    });
  }

  revision(detail: Record<string, unknown>): void {
    const kind = String(detail.kind || "background");
    const urgent = /mutation|operation|removal|failure/.test(kind);
    const stable = document.querySelector('[data-live-policy="stable-list"]');
    if (stable && (kind === "tasks" || kind === "startup")) return;
    if (stable && !urgent) {
      const updates = document.getElementById("updates-available");
      if (updates) updates.hidden = false;
      return;
    }
    this.refreshFragments(urgent);
  }

  refreshFragments(force: boolean): void {
    const fragments = [...document.querySelectorAll<HTMLElement>("[data-reactive-fragment][id]")];
    for (const fragment of fragments) {
      if (fragment.dataset.livePolicy === "stable-list" && !force) continue;
      this.swapFragment(fragment);
    }
    const updates = document.getElementById("updates-available");
    if (updates && force) updates.hidden = true;
  }

  async openOverlay(url: string, opener: HTMLElement): Promise<void> {
    if (this.modalRequest) this.modalRequest.abort();
    this.modalRequest = new AbortController();
    const root = document.getElementById("removal-modal") as RemovalModalRoot | null;
    if (!root) return;
    root.innerHTML = '<div class="removal-overlay"><main class="removal-dialog preparing" role="dialog" aria-modal="true"><p class="muted">Loading…</p><div class="actions"><button type="button" data-modal-cancel-loading>Cancel</button></div></main></div>';
    root.dataset.openerId = opener.id || "";
    root._connarrOpener = opener;
    document.body.classList.add("modal-open");
    try {
      const response = await fetch(url, { signal: this.modalRequest.signal, headers: { "X-Connarr-Overlay": "1" } });
      const content = await response.text();
      if (!response.ok) throw new Error(content.trim() || `status ${response.status}`);
      root.innerHTML = content;
      window.htmx.process(root);
    } catch (error) {
      if ((error as Error).name === "AbortError") return;
      root.innerHTML = `<div class="removal-overlay"><main class="removal-dialog" role="dialog" aria-modal="true"><p class="bad"></p><div class="actions"><button type="button" data-modal-cancel-loading>Close</button></div></main></div>`;
      root.querySelector(".bad")!.textContent = `Unavailable: ${(error as Error).message}`;
    } finally {
      this.modalRequest = null;
    }
  }

  closeModal(): void {
    const root = document.getElementById("removal-modal") as RemovalModalRoot | null;
    if (!root) return;
    const opener = root._connarrOpener;
    root.replaceChildren();
    document.body.classList.remove("modal-open");
    if (opener && document.contains(opener)) opener.focus();
  }
}
