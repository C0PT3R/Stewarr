// Ambient declarations for the two libraries loaded as plain globals before
// this bundle runs (vendored in static/vendor, no npm packages) — see
// templates.go's <head> for the <script> tags that define window.Stimulus
// and window.htmx.

declare class StimulusController {
  element: HTMLElement;
  static targets: string[];
  connect?(): void;
  disconnect?(): void;
}

interface StimulusApplication {
  register(name: string, controllerClass: typeof StimulusController): void;
}

interface HtmxAjaxOptions {
  source?: Element;
  target?: string;
  select?: string;
  swap?: string;
}

interface Window {
  Stimulus: {
    Application: { start(): StimulusApplication };
    Controller: typeof StimulusController;
  };
  htmx: {
    ajax(method: string, url: string, options: HtmxAjaxOptions): Promise<void>;
    process(element: Element): void;
  };
}
