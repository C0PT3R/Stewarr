export class UpdatesController extends window.Stimulus.Controller {
  apply(): void {
    this.element.hidden = true;
    document.dispatchEvent(new CustomEvent("connarr:apply-updates"));
  }
}
