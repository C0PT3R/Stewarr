import { RevisionsController } from "./controllers/revisions";
import { UpdatesController } from "./controllers/updates";
import { DashboardController } from "./controllers/dashboard";
import { ShellController } from "./controllers/shell";
import { RemovalController } from "./controllers/removal";

const application = window.Stimulus.Application.start();
application.register("revisions", RevisionsController);
application.register("updates", UpdatesController);
application.register("dashboard", DashboardController);
application.register("shell", ShellController);
application.register("removal", RemovalController);
