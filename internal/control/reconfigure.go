package control

// Reconfigure serializes a session configuration change with queued/running
// operations and invalidates retained plans. It never restarts a running node.
func (c *Coordinator) Reconfigure(change func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return apiError("session_closed", "Session is closing")
	}
	for _, r := range c.records {
		if r.public.State == "queued" || r.public.State == "running" {
			return apiError("operation_conflict", "Wait for running operations before reloading environment")
		}
	}
	if err := change(); err != nil {
		return err
	}
	c.plans = map[string]storedPlan{}
	return nil
}
