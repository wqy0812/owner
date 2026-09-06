package service

import (
	"context"
)

func (c *executionControl) register(runID string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active[runID] = cancel
}

func (c *executionControl) unregister(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, runID)
}

func (c *executionControl) cancelRun(runID string) bool {
	c.mu.Lock()
	cancel := c.active[runID]
	c.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (c *executionControl) cancelAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.active {
		cancel()
	}
}
