# Development hot reload

Trace the full command chain before changing a registration. A service is only as reloadable as the command or container it starts. StackHarbor's local runner executes the registered argv in its declared cwd and environment and captures output; framework file watching and HMR WebSocket traffic remain owned by that command.

## Diagnose from runtime evidence

1. Read the workspace imports and the selected service's actual command. An unused backend registration cannot affect the workspace. Check the listening process and served HTML: Vite development HTML contains `/@vite/client`; a preview server or static build has a different contract.
2. Inspect the application container's command and mount destinations without printing its environment. `/app/api` with no source mount runs a compiled snapshot. `docker start` and `docker restart` reuse that snapshot; even rebuilding the image alone does not replace an existing container.
3. Compare the normal development entry point. A script that builds once and waits for processes needs an explicit watcher. A framework development command usually supplies its own watcher. Do not add a second restart loop around Vite.
4. Reproduce with a reversible edit to an isolated fixture or a temporary source asset. Verify the new response or browser content without a manual service restart. A healthy endpoint before and after the edit alone does not prove reload.

## Register the development loop

Keep infrastructure such as PostgreSQL/Redis in persistent resources. Register the project's tested foreground application watcher as a service with a loopback readiness probe and stop policy. Preserve existing migrations, seeds and bootstrap boundaries; source changes must not implicitly run schema or data migrations. Before switching application runtimes, identify the exact old applications and port owners. A Docker VM's SSH forwarder is not an application to kill.

When retaining application containers, use the project's explicit development configuration with verified source mounts and a container-side watcher, or its established image rebuild/recreate loop. Existing-container adoption alone cannot supply either. Keep a separately named container workspace if both workflows are useful; they must not start duplicate workers or bind the same API port.

For Go watchers, verify successful edits, embedded assets, compile errors and recovery, bounded graceful replacement, termination during compilation, and cleanup of every owned process. Keep build output and runtime artifacts outside watched source directories. Compile before stopping the previous working version. Configuration changes and database migrations should retain explicit restart/migration instructions.

## Bundled fallback for projects without a watcher

Prefer native framework reload. Otherwise copy [dev_watch.py](../scripts/dev_watch.py) into the target project's owned scripts directory, with a project-owned JSON configuration; never register a command pointing into a user's skill installation or the StackHarbor checkout. The helper uses Python 3.9+ standard library only, performs no package installation, and executes argv without a shell. Keep the copied helper and configuration under the target project's version control.

Example configuration in `scripts/dev-watch.json`:

```json
{
  "services": {
    "api": {
      "cwd": "../backend",
      "watch": ["cmd", "internal", "pkg", "web", "go.mod", "go.sum"],
      "build": ["go", "build", "-o", "{output}/server", "./cmd/server"],
      "run": ["{output}/server"],
      "stop_timeout_seconds": 10
    }
  }
}
```

Run `python3 scripts/dev_watch.py scripts/dev-watch.json api` from the project root; add `--check` for read-only configuration validation. `cwd` is relative to the JSON file; watched paths are relative to that cwd. Include local Go replacement modules and embedded assets. Build output defaults to a private temporary directory; optional top-level `output_root` is relative to the JSON file and must stay outside watched source. Both validation and startup reject an output directory inside a watched path to prevent rebuild loops. Successful builds replace the child; failed builds retain the last good child. A failed initial build waits for another edit. Stops signal the owned process group and enforce a bounded grace period. An application that exits remains stopped until a source edit, with an explicit diagnostic; readiness must still use a real application probe.

For Python background processes, omit `build` and use a `run` argv such as `["{python}", "-B", "-m", "app.worker"]`; `{python}` preserves the helper's interpreter and virtual environment. Each child gets a fresh temporary `PYTHONPYCACHEPREFIX`, preventing stale bytecode after rapid same-size edits. Restarting a coroutine or worker object that still holds imported functions does not prove new source was loaded. Use fresh processes. Optional `pid_file_env` names an environment variable containing the child PID file path for launchers with PID-bound readiness checks; verify the actual child rather than the watcher parent. Environments and registration files retain an explicit manual restart boundary.

For containers, verify the actual Compose version supports Watch. Preserve the ordered deployment inputs, environment, networks, data volumes and role commands, then add a development-only watch override. Use a foreground Compose lifecycle for applications that can be rebuilt/recreated, rather than pinning their changing IDs as adopted resources. Keep infrastructure outside the watched application selection. Build inputs must include only required source and exclude secrets/data. Do not treat a frozen static frontend as the development UI.
