package bridge

import "errors"

var ErrReleaseDrained = errors.New("endpoint drained for a verified release")

type managedRelease struct {
	instance              string
	registered, confirmed func() error
	requests              <-chan struct{}
}

// ConfigureManagedRelease is set by the local, trusted supervisor only.
func (c *Client) ConfigureManagedRelease(instance string, registered, confirmed func() error, requests <-chan struct{}) {
	c.release = &managedRelease{instance: instance, registered: registered, confirmed: confirmed, requests: requests}
}

func (c *Client) releaseIdle() bool {
	c.sessions.mu.Lock()
	// Open chats retain live runner/session state even between prompts.
	chats := len(c.sessions.sessions)
	c.sessions.mu.Unlock()
	c.orchestrations.mu.Lock()
	runs := len(c.orchestrations.runs)
	c.orchestrations.mu.Unlock()
	return chats == 0 && runs == 0
}
