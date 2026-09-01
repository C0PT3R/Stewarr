import { announce, decodeModel, formatBytes } from "../shared";

interface RemovalFile {
  actionName: string;
  actionValue: string;
  selectable?: boolean;
  selected?: boolean;
  always?: boolean;
  owner?: string;
  ownerKey?: string;
  path: string;
  exists?: boolean;
  identityKnown?: boolean;
  device?: number | string;
  inode?: number | string;
  links?: number;
  sizeBytes?: number;
  physicalKey?: string;
}

interface RemovalModel {
  files?: RemovalFile[];
  torrentStatuses?: Record<string, string>;
  mediaType?: string;
  dryRun?: boolean;
}

interface PhysicalGroup {
  size: number;
  links: number;
  known: Set<string>;
  selected: Set<string>;
  facts: RemovalFile[];
}

interface RemovalModalRoot extends HTMLElement {
  _connarrOpener?: HTMLElement;
}

export class RemovalController extends window.Stimulus.Controller {
  static targets = ["selection", "execute", "generatedInputs", "reclaimed", "selectedSummary", "guidance", "guidanceTitle", "guidanceAction", "warnings", "submitButton", "error"];

  // Declared, not implemented: Stimulus injects these getters/setters at
  // runtime from `static targets` above, matching each `data-removal-target`
  // attribute in removal.html.
  declare readonly selectionTarget: HTMLElement;
  declare readonly executeTarget: HTMLFormElement;
  declare readonly hasGeneratedInputsTarget: boolean;
  declare readonly generatedInputsTarget: HTMLElement;
  declare readonly hasReclaimedTarget: boolean;
  declare readonly reclaimedTarget: HTMLElement;
  declare readonly hasSelectedSummaryTarget: boolean;
  declare readonly selectedSummaryTarget: HTMLElement;
  declare readonly hasGuidanceTarget: boolean;
  declare readonly guidanceTarget: HTMLElement;
  declare readonly guidanceTitleTarget: HTMLElement;
  declare readonly guidanceActionTarget: HTMLElement;
  declare readonly hasSubmitButtonTarget: boolean;
  declare readonly submitButtonTarget: HTMLButtonElement;
  declare readonly hasErrorTarget: boolean;
  declare readonly errorTarget: HTMLElement;

  model!: RemovalModel;
  busy = false;
  actionSelections!: Set<string>;
  escapeHandler!: (event: KeyboardEvent) => void;

  connect(): void {
    this.model = decodeModel<RemovalModel>(this.element.dataset.removalModelValue);
    this.busy = false;
    this.actionSelections = new Set((this.model.files || [])
      .filter(file => file.selectable && file.selected)
      .map(file => `${file.actionName}\u0000${file.actionValue}`));
    this.escapeHandler = event => {
      if (event.key === "Escape" && !this.busy) this.cancel();
    };
    document.addEventListener("keydown", this.escapeHandler);
    document.body.classList.add("modal-open");
    this.updateGroupStates();
    this.calculate();
    requestAnimationFrame(() => (this.element.querySelector(".removal-dialog") as HTMLElement | null)?.focus());
  }

  disconnect(): void {
    document.removeEventListener("keydown", this.escapeHandler);
  }

  cancel(): void {
    if (this.busy) return;
    const root = document.getElementById("removal-modal") as RemovalModalRoot | null;
    const opener = root?._connarrOpener;
    root?.replaceChildren();
    document.body.classList.remove("modal-open");
    if (opener && document.contains(opener)) opener.focus();
  }

  backdrop(event: MouseEvent): void {
    if (event.target === this.element && !this.busy) this.cancel();
  }

  selectionChanged(event: Event): void {
    const checkbox = event.target;
    if (!(checkbox instanceof HTMLInputElement) || checkbox.type !== "checkbox") return;
    let selector = "";
    let scope: HTMLElement | null = null;
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
      scope.querySelectorAll<HTMLInputElement>(selector).forEach(input => {
        input.checked = checkbox.checked;
        this.setControlSelection(input, checkbox.checked);
      });
    } else {
      this.setControlSelection(checkbox, checkbox.checked);
    }
    this.updateGroupStates();
    this.calculate();
  }

  setControlSelection(input: HTMLInputElement, selected: boolean): void {
    if (input.matches("[data-physical-pick]")) {
      const physicalKey = input.dataset.physicalKey;
      for (const file of this.model.files || []) {
        if (!file.selectable || file.physicalKey !== physicalKey) continue;
        const key = `${file.actionName}\u0000${file.actionValue}`;
        if (selected) this.actionSelections.add(key);
        else this.actionSelections.delete(key);
      }
      return;
    }
    if (!input.name) return;
    const key = `${input.name}\u0000${input.value}`;
    if (selected) this.actionSelections.add(key);
    else this.actionSelections.delete(key);
  }

  updateGroupStates(): void {
    const setState = (master: HTMLInputElement | null, inputs: HTMLInputElement[]) => {
      if (!master || inputs.length === 0) return;
      const selected = inputs.filter(input => input.checked).length;
      master.checked = selected === inputs.length;
      master.indeterminate = selected > 0 && selected < inputs.length;
    };
    const actions = this.selectedActionKeys();
    this.selectionTarget?.querySelectorAll<HTMLInputElement>('input[name][type="checkbox"]:not([data-generated-input])').forEach(input => {
      input.checked = actions.has(`${input.name}\u0000${input.value}`);
    });
    this.element.querySelectorAll<HTMLInputElement>("[data-physical-pick]").forEach(checkbox => {
      const keys = new Set((this.model.files || [])
        .filter(file => file.selectable && file.physicalKey === checkbox.dataset.physicalKey)
        .map(file => `${file.actionName}\u0000${file.actionValue}`));
      const selected = [...keys].filter(key => actions.has(key)).length;
      if (keys.size === 0) return;
      checkbox.checked = selected === keys.size;
      checkbox.indeterminate = selected > 0 && selected < keys.size;
    });
    this.element.querySelectorAll<HTMLElement>("[data-managed-group]").forEach(scope => setState(scope.querySelector("[data-managed-group-all]"), [...scope.querySelectorAll<HTMLInputElement>("[data-managed-pick]")]));
    this.element.querySelectorAll<HTMLElement>("[data-managed-scope]").forEach(scope => setState(scope.querySelector("[data-managed-all]"), [...scope.querySelectorAll<HTMLInputElement>("[data-managed-pick]")]));
    this.element.querySelectorAll<HTMLElement>("[data-linked-scope]").forEach(scope => setState(scope.querySelector("[data-select-all]"), [...scope.querySelectorAll<HTMLInputElement>("[data-linked-pick]")]));
    this.element.querySelectorAll<HTMLElement>("[data-unmanaged-scope]").forEach(scope => setState(scope.querySelector("[data-unmanaged-all]"), [...scope.querySelectorAll<HTMLInputElement>("[data-unmanaged-pick]")]));
  }

  selectedActionKeys(): Set<string> {
    const selected = new Set(this.actionSelections || []);
    for (const file of this.model.files || []) {
      if (file.always) selected.add(`${file.actionName}\u0000${file.actionValue}`);
    }
    return selected;
  }

  calculate(): void {
    const actions = this.selectedActionKeys();
    const groups = new Map<string, PhysicalGroup>();
    const logicalMedia = new Map<string, number>();
    const selectedFacts: RemovalFile[] = [];
    for (const file of this.model.files || []) {
      const selected = file.always || actions.has(`${file.actionName}\u0000${file.actionValue}`);
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
        group = { size: Number(file.sizeBytes) || 0, links: Number(file.links) || 0, known: new Set(), selected: new Set(), facts: [] };
        groups.set(key, group);
      }
      group.links = Math.max(group.links, Number(file.links) || 0);
      group.known.add(file.path);
      group.facts.push(file);
      if (selected) group.selected.add(file.path);
    }

    let reclaimed = 0;
    const blockers = new Map<string, string>();
    for (const group of groups.values()) {
      if (group.links > 0 && group.selected.size >= group.links) reclaimed += group.size;
      const selectedMedia = group.facts.some(file => file.owner === "media" && file.selected);
      if (selectedMedia && group.selected.size < group.links) {
        for (const file of group.facts) {
          if (file.owner === "torrent" && !file.selected) blockers.set(String(file.ownerKey).toLowerCase(), this.model.torrentStatuses?.[String(file.ownerKey).toLowerCase()] || "UNASSOCIATED");
        }
      }
    }

    if (this.hasReclaimedTarget) this.reclaimedTarget.textContent = formatBytes(reclaimed);
    if (this.hasSelectedSummaryTarget) {
      const count = selectedFacts.filter(file => file.owner === "media").length;
      const bytes = [...logicalMedia.values()].reduce((sum, size) => sum + size, 0);
      this.selectedSummaryTarget.textContent = count ? `${count} file${count === 1 ? "" : "s"} selected · ${formatBytes(bytes)}` : "";
    }
    this.element.querySelectorAll<HTMLElement>("[data-physical-owner]").forEach(row => {
      const name = row.dataset.actionName;
      const value = row.dataset.actionValue;
      const state = row.querySelector("[data-physical-action-state]");
      if (!state || !name) return;
      state.textContent = actions.has(`${name}\u0000${value}`) ? row.dataset.actionLabel! : "Preserved";
    });
    this.renderGuidance(blockers, reclaimed, selectedFacts.filter(file => file.owner === "media").length);
    this.syncExecutionInputs(actions);
    if (this.hasSubmitButtonTarget) this.submitButtonTarget.disabled = actions.size === 0 || this.busy;
  }

  renderGuidance(blockers: Map<string, string>, reclaimed: number, selectedFiles: number): void {
    if (!this.hasGuidanceTarget) return;
    if (blockers.size === 0) {
      this.guidanceTarget.hidden = true;
      return;
    }
    let fileName = this.model.mediaType === "movie" ? "movie file" : this.model.mediaType === "series" ? "episode file" : "library file";
    const plural = selectedFiles !== 1;
    if (plural) fileName += "s";
    const possessive = plural ? "their" : "its";
    this.guidanceTitleTarget.textContent = reclaimed === 0
      ? `Removing the selected ${fileName} alone will not reclaim storage space.`
      : `Removing the selected ${fileName} alone will not reclaim all of ${possessive} storage space.`;
    const allCurrent = [...blockers.values()].every(status => status === "CURRENT" || status === "ASSOCIATED");
    if (blockers.size === 1 && allCurrent) this.guidanceActionTarget.textContent = `${plural ? "They are" : "It is"} hardlinked to the current torrent. Also select that torrent below to reclaim the shared data.`;
    else if (allCurrent) this.guidanceActionTarget.textContent = "They are hardlinked to current torrents. Also select those torrents below to reclaim the shared data.";
    else this.guidanceActionTarget.textContent = "They are hardlinked to related torrents. Also select those torrents below to reclaim the shared data.";
    this.guidanceTarget.hidden = false;
  }

  syncExecutionInputs(actions: Set<string>): void {
    if (!this.hasGeneratedInputsTarget) return;
    this.generatedInputsTarget.replaceChildren();
    for (const key of actions) {
      const split = key.indexOf("\u0000");
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = key.slice(0, split);
      input.value = key.slice(split + 1);
      input.dataset.generatedInput = "";
      this.generatedInputsTarget.append(input);
    }
  }

  async submit(event: Event): Promise<void> {
    event.preventDefault();
    if (this.busy || this.submitButtonTarget.disabled) return;
    this.busy = true;
    this.submitButtonTarget.disabled = true;
    this.submitButtonTarget.textContent = this.model.dryRun ? "Simulating…" : "Queueing…";
    if (this.hasErrorTarget) this.errorTarget.hidden = true;
    try {
      const response = await fetch(this.executeTarget.action, {
        method: "POST",
        body: new FormData(this.executeTarget),
        headers: { "Accept": "application/json", "X-Connarr-Overlay": "1" }
      });
      const responseText = await response.text();
      let body: { error?: string; message?: string } = {};
      try { body = JSON.parse(responseText); } catch (_) { body = { error: responseText }; }
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
        this.errorTarget.textContent = (error as Error).message;
        this.errorTarget.hidden = false;
      }
    }
  }

  cancelAfterAcceptance(): void {
    const root = document.getElementById("removal-modal");
    root?.replaceChildren();
    document.body.classList.remove("modal-open");
  }
}
