// Package cleanup provides resource cleanup utilities.
package cleanup

import (
	"context"
	"fmt"
	"sync"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/deployer"
)

// Manager tracks deployed resources and cleans them up.
type Manager struct {
	mu        sync.Mutex
	deployer  *deployer.Deployer
	deployed  []*config.TestCase
}

// NewManager creates a cleanup Manager.
func NewManager(d *deployer.Deployer) *Manager {
	return &Manager{deployer: d}
}

// Track registers a test case for cleanup.
func (m *Manager) Track(tc *config.TestCase) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deployed = append(m.deployed, tc)
}

// Remove unregisters a test case (e.g., after manual cleanup).
func (m *Manager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, tc := range m.deployed {
		if tc.Name == name {
			m.deployed = append(m.deployed[:i], m.deployed[i+1:]...)
			return
		}
	}
}

// CleanupAll cleans up all tracked resources. Returns a slice of errors for any failures.
func (m *Manager) CleanupAll(ctx context.Context) []error {
	m.mu.Lock()
	cases := make([]*config.TestCase, len(m.deployed))
	copy(cases, m.deployed)
	m.mu.Unlock()

	var errs []error
	for _, tc := range cases {
		if err := m.deployer.Cleanup(ctx, tc); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s: %w", tc.Name, err))
		}
	}

	m.mu.Lock()
	m.deployed = nil
	m.mu.Unlock()

	return errs
}

// CleanupOne cleans up a single test case by name.
func (m *Manager) CleanupOne(ctx context.Context, name string) error {
	m.mu.Lock()
	var target *config.TestCase
	for _, tc := range m.deployed {
		if tc.Name == name {
			target = tc
			break
		}
	}
	m.mu.Unlock()

	if target == nil {
		return fmt.Errorf("test case %s not tracked for cleanup", name)
	}

	if err := m.deployer.Cleanup(ctx, target); err != nil {
		return err
	}

	m.Remove(name)
	return nil
}
