"use strict";
(() => {
  // internal/httpui/static/src/shared.ts
  function announce(message) {
    const region = document.getElementById("ui-announcer");
    if (region) region.textContent = message;
  }
  function formatBytes(value) {
    let bytes = Math.max(0, Number(value) || 0);
    const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    let unit = 0;
    while (bytes >= 1024 && unit < units.length - 1) {
      bytes /= 1024;
      unit += 1;
    }
    const digits = unit === 0 ? 0 : bytes >= 10 ? 1 : 2;
    return `${bytes.toFixed(digits)} ${units[unit]}`;
  }
  function decodeModel(encoded) {
    const binary = atob(encoded || "");
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes));
  }
  function dispatchRevision(detail) {
    document.dispatchEvent(new CustomEvent("connarr:revision", { detail }));
  }

  // internal/httpui/static/src/controllers/revisions.ts
  var RevisionsController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.etag = "";
      this.pollTimer = null;
      this.events = null;
    }
    connect() {
      this.etag = "";
      this.pollTimer = null;
      this.onVisible = () => {
        if (!document.hidden) this.refreshStatus({ kind: "visibility" });
      };
      document.addEventListener("visibilitychange", this.onVisible);
      this.connectEvents();
    }
    disconnect() {
      document.removeEventListener("visibilitychange", this.onVisible);
      if (this.events) this.events.close();
      if (this.pollTimer) clearInterval(this.pollTimer);
    }
    connectEvents() {
      if (!("EventSource" in window)) {
        this.startPolling();
        return;
      }
      this.events = new EventSource("/ui/events");
      this.events.addEventListener("revision", (event) => {
        try {
          this.refreshStatus(JSON.parse(event.data));
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
    startPolling() {
      if (this.pollTimer) return;
      this.pollTimer = setInterval(() => {
        if (!document.hidden) this.refreshStatus({ kind: "poll" });
      }, 5e3);
    }
    async refreshStatus(invalidation) {
      const headers = {};
      if (this.etag) headers["If-None-Match"] = this.etag;
      try {
        const response = await fetch("/ui/status", { headers, cache: "no-store" });
        if (response.status === 304) return;
        if (!response.ok) throw new Error(`status ${response.status}`);
        this.etag = response.headers.get("ETag") || "";
        const status = await response.json();
        this.renderStatus(status);
        const sourceKind = invalidation.kind === "poll" || invalidation.kind === "visibility" ? status.kind : invalidation.kind;
        dispatchRevision({ ...status, kind: sourceKind || status.kind });
      } catch (_) {
        this.startPolling();
      }
    }
    renderStatus(status) {
      const indicator = document.getElementById("operation-indicator");
      if (indicator) {
        const count = Number(status.pendingOperations) || 0;
        indicator.hidden = count === 0;
        indicator.textContent = count === 1 ? "1 operation in progress" : `${count} operations in progress`;
      }
      const notices = document.getElementById("persistent-notices");
      if (notices) {
        notices.replaceChildren(...(status.notices || []).map((notice) => {
          const item = document.createElement("a");
          item.className = "persistent-notice bad";
          item.href = `/history#operation-${notice.id}`;
          item.textContent = `${notice.label || "Removal"}: ${notice.message || notice.status}`;
          return item;
        }));
      }
    }
  };

  // internal/httpui/static/src/controllers/updates.ts
  var UpdatesController = class extends window.Stimulus.Controller {
    apply() {
      this.element.hidden = true;
      document.dispatchEvent(new CustomEvent("connarr:apply-updates"));
    }
  };

  // internal/httpui/static/src/controllers/dashboard.ts
  var DashboardController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.etag = "";
    }
    connect() {
      this.etag = "";
      this.onRevision = (event) => {
        const kind = String(event.detail?.kind || "background");
        if (kind === "tasks") return;
        this.refresh();
      };
      document.addEventListener("connarr:revision", this.onRevision);
    }
    disconnect() {
      document.removeEventListener("connarr:revision", this.onRevision);
    }
    field(name) {
      return this.element.querySelector(`[data-dashboard-field="${name}"]`);
    }
    set(name, value) {
      const element = this.field(name);
      if (element) element.textContent = String(value);
    }
    async refresh() {
      const headers = {};
      if (this.etag) headers["If-None-Match"] = this.etag;
      try {
        const response = await fetch("/api/dashboard", { headers, cache: "no-store" });
        if (response.status === 304) return;
        if (!response.ok) throw new Error(`status ${response.status}`);
        this.etag = response.headers.get("ETag") || "";
        const data = await response.json();
        for (const name of ["totalMedia", "movies", "series", "libraryBytes", "totalTorrents", "current", "superseded", "unassociated", "obsoleteReclaimable"]) this.set(name, data[name]);
        const stats = data.stats || {};
        this.set("reclaimedBytes", formatBytes(stats.ReclaimedBytes));
        this.set("runs", stats.Runs || 0);
        this.set("mediaRemoved", stats.MediaRemoved || 0);
        this.set("torrentsRemoved", stats.TorrentsRemoved || 0);
        this.set("mediaBytes", formatBytes(stats.MediaBytes));
        this.set("last30Bytes", formatBytes(stats.Last30Bytes));
      } catch (_) {
      }
    }
  };

  // internal/httpui/static/src/controllers/shell.ts
  var ShellController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.filterTimer = null;
      this.modalRequest = null;
    }
    connect() {
      this.filterTimer = null;
      this.modalRequest = null;
      this.onClick = (event) => this.click(event);
      this.onInput = (event) => this.filterInput(event);
      this.onChange = (event) => this.filterChange(event);
      this.onSubmit = (event) => this.submit(event);
      this.onRevision = (event) => this.revision(event.detail || {});
      this.onApply = () => this.refreshFragments(true);
      this.onAccepted = () => this.refreshFragments(true);
      document.addEventListener("click", this.onClick);
      document.addEventListener("input", this.onInput);
      document.addEventListener("change", this.onChange);
      document.addEventListener("submit", this.onSubmit);
      document.addEventListener("connarr:revision", this.onRevision);
      document.addEventListener("connarr:apply-updates", this.onApply);
      document.addEventListener("connarr:mutation-accepted", this.onAccepted);
    }
    disconnect() {
      document.removeEventListener("click", this.onClick);
      document.removeEventListener("input", this.onInput);
      document.removeEventListener("change", this.onChange);
      document.removeEventListener("submit", this.onSubmit);
      document.removeEventListener("connarr:revision", this.onRevision);
      document.removeEventListener("connarr:apply-updates", this.onApply);
      document.removeEventListener("connarr:mutation-accepted", this.onAccepted);
      if (this.filterTimer) clearTimeout(this.filterTimer);
      if (this.modalRequest) this.modalRequest.abort();
    }
    click(event) {
      const target = event.target;
      const removal = target.closest("[data-removal-url]");
      if (removal) {
        event.preventDefault();
        this.openRemoval(removal.dataset.removalUrl, removal);
        return;
      }
      const loadingCancel = target.closest("[data-modal-cancel-loading]");
      if (loadingCancel) {
        event.preventDefault();
        if (this.modalRequest) this.modalRequest.abort();
        this.closeModal();
        return;
      }
      const clear = target.closest("[data-filter-clear]");
      if (clear) {
        event.preventDefault();
        const form = clear.closest("body")?.querySelector("form[data-auto-filter]");
        if (form) {
          form.reset();
          this.navigateList(clear.href);
        }
        return;
      }
      const listLink = target.closest("[data-filter-results] a[href]:not([data-list-item-link])");
      if (listLink && listLink.origin === location.origin) {
        event.preventDefault();
        this.navigateList(listLink.href);
        return;
      }
      const scan = target.closest("[data-unmanaged-scan]");
      if (scan) {
        event.preventDefault();
        this.scanUnmanaged(scan);
        return;
      }
      const all = target.closest("#unmanagedAll");
      if (all) {
        document.querySelectorAll(".unmanagedPick").forEach((input) => {
          input.checked = all.checked;
        });
        this.syncUnmanagedSelection();
      }
    }
    filterInput(event) {
      const target = event.target;
      const form = target.closest("form[data-auto-filter]");
      if (!form || target.type !== "search") return;
      if (this.filterTimer) clearTimeout(this.filterTimer);
      this.filterTimer = setTimeout(() => this.navigateList(this.formURL(form)), 180);
    }
    filterChange(event) {
      const target = event.target;
      if (target.matches(".unmanagedPick")) {
        this.syncUnmanagedSelection();
        return;
      }
      const form = target.closest("form[data-auto-filter]");
      if (form) this.navigateList(this.formURL(form));
    }
    async submit(event) {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (form.matches("[data-removal-launch]")) {
        event.preventDefault();
        const url = new URL(form.action, location.href);
        for (const [name, value] of new FormData(form)) url.searchParams.append(name, value);
        this.openRemoval(url.href, form.querySelector("button[type=submit]"));
        return;
      }
      if (form.matches("[data-background-submit]")) {
        event.preventDefault();
        const button = form.querySelector("button[type=submit]");
        if (button) button.disabled = true;
        try {
          const response = await fetch(form.action, { method: form.method || "POST", body: new FormData(form) });
          if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
          dispatchRevision({ kind: "operation" });
        } catch (error) {
          announce(`Action failed: ${error.message}`);
        } finally {
          if (button) button.disabled = false;
        }
      }
    }
    syncUnmanagedSelection() {
      const picks = [...document.querySelectorAll(".unmanagedPick")];
      for (const pick of picks) {
        document.querySelectorAll(`.unmanagedGroupPath[data-unmanaged-group="${CSS.escape(pick.dataset.unmanagedGroup)}"]`).forEach((input) => {
          input.disabled = !pick.checked;
        });
      }
      const selected = picks.filter((input) => input.checked).length;
      const all = document.getElementById("unmanagedAll");
      if (all) {
        all.checked = picks.length > 0 && selected === picks.length;
        all.indeterminate = selected > 0 && selected < picks.length;
      }
      const button = document.getElementById("unmanagedRemoveButton");
      if (button) button.disabled = selected === 0;
    }
    async scanUnmanaged(button) {
      const old = button.textContent;
      button.disabled = true;
      button.textContent = "Scanning\u2026";
      try {
        const response = await fetch("/downloads/unmanaged/scan", { method: "POST", headers: { "X-Connarr-Scan": "1" } });
        const result = await response.json();
        if (!response.ok || !result.ok) throw new Error(result.error || `status ${response.status}`);
        this.refreshFragments(true);
        announce("Unmanaged file scan completed.");
      } catch (error) {
        announce(`Unmanaged scan failed: ${error.message}`);
      } finally {
        button.disabled = false;
        button.textContent = old;
      }
    }
    formURL(form) {
      const url = new URL(form.action, location.href);
      const values = new FormData(form);
      for (const [name, value] of values) url.searchParams.append(name, value);
      return url.href;
    }
    navigateList(url) {
      const fragment = document.querySelector("[data-filter-results]");
      if (!fragment || !fragment.id) return;
      history.replaceState({}, "", url);
      this.swapFragment(fragment, url);
      const updates = document.getElementById("updates-available");
      if (updates) updates.hidden = true;
    }
    swapFragment(fragment, url = location.href) {
      return window.htmx.ajax("GET", url, {
        source: fragment,
        target: `#${CSS.escape(fragment.id)}`,
        select: `#${CSS.escape(fragment.id)}`,
        swap: "outerHTML"
      });
    }
    revision(detail) {
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
    refreshFragments(force) {
      const fragments = [...document.querySelectorAll("[data-reactive-fragment][id]")];
      for (const fragment of fragments) {
        if (fragment.dataset.livePolicy === "stable-list" && !force) continue;
        this.swapFragment(fragment);
      }
      const updates = document.getElementById("updates-available");
      if (updates && force) updates.hidden = true;
    }
    async openRemoval(url, opener) {
      if (this.modalRequest) this.modalRequest.abort();
      this.modalRequest = new AbortController();
      const root = document.getElementById("removal-modal");
      if (!root) return;
      root.innerHTML = '<div class="removal-overlay"><main class="removal-dialog preparing" role="dialog" aria-modal="true"><p class="muted">Preparing removal plan\u2026</p><div class="actions"><button type="button" data-modal-cancel-loading>Cancel</button></div></main></div>';
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
        if (error.name === "AbortError") return;
        root.innerHTML = `<div class="removal-overlay"><main class="removal-dialog" role="dialog" aria-modal="true"><p class="bad"></p><div class="actions"><button type="button" data-modal-cancel-loading>Close</button></div></main></div>`;
        root.querySelector(".bad").textContent = `Removal plan unavailable: ${error.message}`;
      } finally {
        this.modalRequest = null;
      }
    }
    closeModal() {
      const root = document.getElementById("removal-modal");
      if (!root) return;
      const opener = root._connarrOpener;
      root.replaceChildren();
      document.body.classList.remove("modal-open");
      if (opener && document.contains(opener)) opener.focus();
    }
  };

  // internal/httpui/static/src/controllers/removal.ts
  var RemovalController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.busy = false;
    }
    connect() {
      this.model = decodeModel(this.element.dataset.removalModelValue);
      this.busy = false;
      this.actionSelections = new Set((this.model.files || []).filter((file) => file.selectable && file.selected).map((file) => `${file.actionName}\0${file.actionValue}`));
      this.escapeHandler = (event) => {
        if (event.key === "Escape" && !this.busy) this.cancel();
      };
      document.addEventListener("keydown", this.escapeHandler);
      document.body.classList.add("modal-open");
      this.updateGroupStates();
      this.calculate();
      requestAnimationFrame(() => this.element.querySelector(".removal-dialog")?.focus());
    }
    disconnect() {
      document.removeEventListener("keydown", this.escapeHandler);
    }
    cancel() {
      if (this.busy) return;
      const root = document.getElementById("removal-modal");
      const opener = root?._connarrOpener;
      root?.replaceChildren();
      document.body.classList.remove("modal-open");
      if (opener && document.contains(opener)) opener.focus();
    }
    backdrop(event) {
      if (event.target === this.element && !this.busy) this.cancel();
    }
    selectionChanged(event) {
      const checkbox = event.target;
      if (!(checkbox instanceof HTMLInputElement) || checkbox.type !== "checkbox") return;
      let selector = "";
      let scope = null;
      if (checkbox.matches("[data-managed-group-all]")) {
        scope = checkbox.closest("[data-managed-group]");
        selector = "[data-managed-pick]";
      } else if (checkbox.matches("[data-managed-all]")) {
        scope = checkbox.closest("[data-managed-scope]");
        selector = "[data-managed-pick]";
      } else if (checkbox.matches("[data-select-all]")) {
        scope = checkbox.closest("[data-linked-scope]");
        selector = "[data-linked-pick]";
      } else if (checkbox.matches("[data-unmanaged-all]")) {
        scope = checkbox.closest("[data-unmanaged-scope]");
        selector = "[data-unmanaged-pick]";
      }
      if (scope && selector) {
        scope.querySelectorAll(selector).forEach((input) => {
          input.checked = checkbox.checked;
          this.setControlSelection(input, checkbox.checked);
        });
      } else {
        this.setControlSelection(checkbox, checkbox.checked);
      }
      this.updateGroupStates();
      this.calculate();
    }
    setControlSelection(input, selected) {
      if (input.matches("[data-physical-pick]")) {
        const physicalKey = input.dataset.physicalKey;
        for (const file of this.model.files || []) {
          if (!file.selectable || file.physicalKey !== physicalKey) continue;
          const key2 = `${file.actionName}\0${file.actionValue}`;
          if (selected) this.actionSelections.add(key2);
          else this.actionSelections.delete(key2);
        }
        return;
      }
      if (!input.name) return;
      const key = `${input.name}\0${input.value}`;
      if (selected) this.actionSelections.add(key);
      else this.actionSelections.delete(key);
    }
    updateGroupStates() {
      const setState = (master, inputs) => {
        if (!master || inputs.length === 0) return;
        const selected = inputs.filter((input) => input.checked).length;
        master.checked = selected === inputs.length;
        master.indeterminate = selected > 0 && selected < inputs.length;
      };
      const actions = this.selectedActionKeys();
      this.selectionTarget?.querySelectorAll('input[name][type="checkbox"]:not([data-generated-input])').forEach((input) => {
        input.checked = actions.has(`${input.name}\0${input.value}`);
      });
      this.element.querySelectorAll("[data-physical-pick]").forEach((checkbox) => {
        const keys = new Set((this.model.files || []).filter((file) => file.selectable && file.physicalKey === checkbox.dataset.physicalKey).map((file) => `${file.actionName}\0${file.actionValue}`));
        const selected = [...keys].filter((key) => actions.has(key)).length;
        if (keys.size === 0) return;
        checkbox.checked = selected === keys.size;
        checkbox.indeterminate = selected > 0 && selected < keys.size;
      });
      this.element.querySelectorAll("[data-managed-group]").forEach((scope) => setState(scope.querySelector("[data-managed-group-all]"), [...scope.querySelectorAll("[data-managed-pick]")]));
      this.element.querySelectorAll("[data-managed-scope]").forEach((scope) => setState(scope.querySelector("[data-managed-all]"), [...scope.querySelectorAll("[data-managed-pick]")]));
      this.element.querySelectorAll("[data-linked-scope]").forEach((scope) => setState(scope.querySelector("[data-select-all]"), [...scope.querySelectorAll("[data-linked-pick]")]));
      this.element.querySelectorAll("[data-unmanaged-scope]").forEach((scope) => setState(scope.querySelector("[data-unmanaged-all]"), [...scope.querySelectorAll("[data-unmanaged-pick]")]));
    }
    selectedActionKeys() {
      const selected = new Set(this.actionSelections || []);
      for (const file of this.model.files || []) {
        if (file.always) selected.add(`${file.actionName}\0${file.actionValue}`);
      }
      return selected;
    }
    calculate() {
      const actions = this.selectedActionKeys();
      const groups = /* @__PURE__ */ new Map();
      const logicalMedia = /* @__PURE__ */ new Map();
      const selectedFacts = [];
      for (const file of this.model.files || []) {
        const selected = file.always || actions.has(`${file.actionName}\0${file.actionValue}`);
        file.selected = selected;
        if (selected) selectedFacts.push(file);
        if (selected && file.owner === "media") {
          const logicalKey = file.identityKnown ? `${file.device}:${file.inode}` : file.path;
          if (!logicalMedia.has(logicalKey)) logicalMedia.set(logicalKey, Number(file.sizeBytes) || 0);
        }
        if (!file.exists || !file.identityKnown) continue;
        const key = `${file.device}:${file.inode}`;
        let group = groups.get(key);
        if (!group) {
          group = { size: Number(file.sizeBytes) || 0, links: Number(file.links) || 0, known: /* @__PURE__ */ new Set(), selected: /* @__PURE__ */ new Set(), facts: [] };
          groups.set(key, group);
        }
        group.links = Math.max(group.links, Number(file.links) || 0);
        group.known.add(file.path);
        group.facts.push(file);
        if (selected) group.selected.add(file.path);
      }
      let reclaimed = 0;
      const blockers = /* @__PURE__ */ new Map();
      for (const group of groups.values()) {
        if (group.links > 0 && group.selected.size >= group.links) reclaimed += group.size;
        const selectedMedia = group.facts.some((file) => file.owner === "media" && file.selected);
        if (selectedMedia && group.selected.size < group.links) {
          for (const file of group.facts) {
            if (file.owner === "torrent" && !file.selected) blockers.set(String(file.ownerKey).toLowerCase(), this.model.torrentStatuses?.[String(file.ownerKey).toLowerCase()] || "UNASSOCIATED");
          }
        }
      }
      if (this.hasReclaimedTarget) this.reclaimedTarget.textContent = formatBytes(reclaimed);
      if (this.hasSelectedSummaryTarget) {
        const count = selectedFacts.filter((file) => file.owner === "media").length;
        const bytes = [...logicalMedia.values()].reduce((sum, size) => sum + size, 0);
        this.selectedSummaryTarget.textContent = count ? `${count} file${count === 1 ? "" : "s"} selected \xB7 ${formatBytes(bytes)}` : "";
      }
      this.element.querySelectorAll("[data-physical-owner]").forEach((row) => {
        const name = row.dataset.actionName;
        const value = row.dataset.actionValue;
        const state = row.querySelector("[data-physical-action-state]");
        if (!state || !name) return;
        state.textContent = actions.has(`${name}\0${value}`) ? row.dataset.actionLabel : "Preserved";
      });
      this.renderGuidance(blockers, reclaimed, selectedFacts.filter((file) => file.owner === "media").length);
      this.syncExecutionInputs(actions);
      if (this.hasSubmitButtonTarget) this.submitButtonTarget.disabled = actions.size === 0 || this.busy;
    }
    renderGuidance(blockers, reclaimed, selectedFiles) {
      if (!this.hasGuidanceTarget) return;
      if (blockers.size === 0) {
        this.guidanceTarget.hidden = true;
        return;
      }
      let fileName = this.model.mediaType === "movie" ? "movie file" : this.model.mediaType === "series" ? "episode file" : "library file";
      const plural = selectedFiles !== 1;
      if (plural) fileName += "s";
      const possessive = plural ? "their" : "its";
      this.guidanceTitleTarget.textContent = reclaimed === 0 ? `Removing the selected ${fileName} alone will not reclaim storage space.` : `Removing the selected ${fileName} alone will not reclaim all of ${possessive} storage space.`;
      const allCurrent = [...blockers.values()].every((status) => status === "CURRENT" || status === "ASSOCIATED");
      if (blockers.size === 1 && allCurrent) this.guidanceActionTarget.textContent = `${plural ? "They are" : "It is"} hardlinked to the current torrent. Also select that torrent below to reclaim the shared data.`;
      else if (allCurrent) this.guidanceActionTarget.textContent = "They are hardlinked to current torrents. Also select those torrents below to reclaim the shared data.";
      else this.guidanceActionTarget.textContent = "They are hardlinked to related torrents. Also select those torrents below to reclaim the shared data.";
      this.guidanceTarget.hidden = false;
    }
    syncExecutionInputs(actions) {
      if (!this.hasGeneratedInputsTarget) return;
      this.generatedInputsTarget.replaceChildren();
      for (const key of actions) {
        const split = key.indexOf("\0");
        const input = document.createElement("input");
        input.type = "hidden";
        input.name = key.slice(0, split);
        input.value = key.slice(split + 1);
        input.dataset.generatedInput = "";
        this.generatedInputsTarget.append(input);
      }
    }
    async submit(event) {
      event.preventDefault();
      if (this.busy || this.submitButtonTarget.disabled) return;
      this.busy = true;
      this.submitButtonTarget.disabled = true;
      this.submitButtonTarget.textContent = this.model.dryRun ? "Simulating\u2026" : "Queueing\u2026";
      if (this.hasErrorTarget) this.errorTarget.hidden = true;
      try {
        const response = await fetch(this.executeTarget.action, {
          method: "POST",
          body: new FormData(this.executeTarget),
          headers: { "Accept": "application/json", "X-Connarr-Overlay": "1" }
        });
        const responseText = await response.text();
        let body = {};
        try {
          body = JSON.parse(responseText);
        } catch (_) {
          body = { error: responseText };
        }
        if (!response.ok) throw new Error(body.error || body.message || `status ${response.status}`);
        const label = this.element.querySelector("#removal-title")?.nextElementSibling?.textContent?.trim() || "Removal";
        this.cancelAfterAcceptance();
        announce(`${label} queued for removal.`);
        document.dispatchEvent(new CustomEvent("connarr:mutation-accepted", { detail: body }));
      } catch (error) {
        this.busy = false;
        this.submitButtonTarget.textContent = this.model.dryRun ? "Simulate" : "Remove selected";
        this.calculate();
        if (this.hasErrorTarget) {
          this.errorTarget.textContent = error.message;
          this.errorTarget.hidden = false;
        }
      }
    }
    cancelAfterAcceptance() {
      const root = document.getElementById("removal-modal");
      root?.replaceChildren();
      document.body.classList.remove("modal-open");
    }
  };
  RemovalController.targets = ["selection", "execute", "generatedInputs", "reclaimed", "selectedSummary", "guidance", "guidanceTitle", "guidanceAction", "warnings", "submitButton", "error"];

  // internal/httpui/static/src/app.ts
  var application = window.Stimulus.Application.start();
  application.register("revisions", RevisionsController);
  application.register("updates", UpdatesController);
  application.register("dashboard", DashboardController);
  application.register("shell", ShellController);
  application.register("removal", RemovalController);
})();
