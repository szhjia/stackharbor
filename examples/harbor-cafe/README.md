# Harbor Café

A fictional café workspace created specifically for the StackHarbor walkthrough. It uses two real Go standard-library HTTP servers and no external database or Docker engine.

- **Menu API** serves three drinks on `127.0.0.1:18281`.
- **Order counter** reads that menu and serves a page on `127.0.0.1:18282`.
- The order counter depends on the menu API's readiness. Its own health endpoint checks that it can still read the menu.

From the StackHarbor source root, run `make demo`. Press Shift+S to start both services in dependency order. Select Order counter with the down arrow; use `o` to open the page and `i` for execution details. Select Menu API and request stop to review the affected downstream service. Press `q` to stop both session-owned processes and exit.

The example can be copied elsewhere and opened with `stackharbor --root PATH`. Go 1.26+ is required for the example commands. If a port is occupied, cancel conflict release and update both the Go listener addresses and YAML declarations. This is a demonstration, not a production ordering system.
