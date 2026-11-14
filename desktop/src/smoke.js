// Compiled only into debug hosts and injected only for an explicitly configured
// isolated native fixture. All operations still traverse the real IPC handler.
(async () => {
  const checks = [];
  const invoke = (command, args = {}) => window.__TAURI_INTERNALS__.invoke(command, args);
  const check = (condition, name) => { if (!condition) throw Error(name); checks.push(name); };
  const rejects = async (command, args) => { try { await invoke(command, args); return false; } catch { return true; } };
  try {
    if (__SMOKE_PHASE__ === 'install') {
      const initial = await invoke('desktop_status');
      check(!initial.complete && !initial.agreement.accepted, 'first-launch-requires-agreement');
      check(await rejects('desktop_set_autostart', {enabled: true}), 'autostart-before-agreement-refused');
      check(await rejects('desktop_uninstall'), 'uninstall-before-agreement-refused');
      const agreement = await invoke('desktop_agreement');
      check(agreement.text.includes('MIT License') && agreement.text.includes('managed PATH') && agreement.publisher.includes('HengKe'), 'full-agreement-publisher-license-preview');
      const options = {acceptAgreement: true, agreementHash: agreement.hash, autoStart: false, migrateConflicts: false, configurePath: true};
      check(await rejects('desktop_setup', {...options, acceptAgreement: false}), 'declined-agreement-refused');
      const conflict = await invoke('desktop_setup', options);
      check(!conflict.complete && conflict.conflicts.length > 0, 'unowned-cli-preserved-with-conflict');
      const installed = await invoke('desktop_setup', {...options, migrateConflicts: true});
      check(installed.complete && installed.path_configured && installed.background.status === 'enabled' && !installed.autostart.enabled, 'mandatory-install-verified-default-login-off');
      check((await invoke('desktop_setup', options)).complete, 'setup-idempotent-resume');
      check((await invoke('desktop_set_autostart', {enabled: true})).autostart.enabled, 'login-enable-status');
      check(!(await invoke('desktop_set_autostart', {enabled: false})).autostart.enabled, 'login-disable-status');
      const response = await invoke('desktop_request', {method: 'GET', path: '/api/v1/workspaces'});
      check(response.status === 200 && JSON.parse(response.body).data !== undefined, 'real-private-engine-request');
      check(await rejects('desktop_request', {method: 'GET', path: '/engine/v1/shutdown'}), 'lifecycle-route-invoke-refused');
      check(await rejects('desktop_request', {method: 'GET', path: '/api/v1/%2e%2e/secret'}), 'traversal-invoke-refused');
      let first;
      const received = new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(Error('No native SSE event')), 8000);
        first = window.__TAURI_INTERNALS__.transformCallback(raw => {
          if (raw.message) { clearTimeout(timer); resolve(raw.message); }
        }, false);
      });
      const subscription = await invoke('desktop_subscribe', {path: '/api/v1/events', onEvent: `__CHANNEL__:${first}`});
      const event = await received;
      check(typeof event.event === 'string' && typeof event.data === 'string' && event.event !== 'error', 'real-private-engine-event-channel');
      await invoke('desktop_unsubscribe', {id: subscription});
      window.__TAURI_INTERNALS__.unregisterCallback(first);
      checks.push('native-stream-unsubscribe');
      await new Promise(resolve => setTimeout(resolve, 2000));
    } else {
      const removed = await invoke('desktop_uninstall');
      check(!removed.complete && removed.background.status === 'not_registered' && !removed.autostart.enabled, 'idle-uninstall-unregisters-fixture-services');
      check(removed.issues.some(issue => issue.includes('Modified entry preserved')), 'modified-skill-preserved');
    }
    await invoke('desktop_smoke_complete', {token: __SMOKE_TOKEN__, result: {ok: true, phase: __SMOKE_PHASE__, checks, namespace: window.__STACKHARBOR_DESKTOP__.namespace}});
  } catch (error) {
    await invoke('desktop_smoke_complete', {token: __SMOKE_TOKEN__, result: {ok: false, phase: __SMOKE_PHASE__, checks, error: String(error)}});
  }
})();
