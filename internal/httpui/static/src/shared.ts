export function announce(message: string): void {
  const region = document.getElementById("ui-announcer");
  if (region) region.textContent = message;
}

export function formatBytes(value: unknown): string {
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

export function decodeModel<T = unknown>(encoded: string | undefined): T {
  const binary = atob(encoded || "");
  const bytes = Uint8Array.from(binary, character => character.charCodeAt(0));
  return JSON.parse(new TextDecoder().decode(bytes));
}

export function dispatchRevision(detail: Record<string, unknown>): void {
  document.dispatchEvent(new CustomEvent("connarr:revision", { detail }));
}
