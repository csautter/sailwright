package gateway

import (
	"context"
	"fmt"
	"strings"
)

// reserveInstance counts provider-visible VMs and in-flight creates. Including
// operator VMs is conservative: the budget protects host resources across data
// directories. The mutex closes the race between simultaneous create jobs.
func (g *Gateway) reserveInstance(ctx context.Context, target VMTarget) (func(), error) {
	if g.cfg.MaxInstances <= 0 {
		return func() {}, nil
	}
	g.instanceMu.Lock()
	defer g.instanceMu.Unlock()
	output, code, err := g.runInfo(ctx, []string{"create", "list", "--json"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("cannot check VM instance budget: create list exited %d", code)
	}
	if strings.Contains(output, "cannot discover named VM instances") {
		return nil, fmt.Errorf("cannot check VM instance budget: provider discovery failed")
	}
	targets, err := ParseTargetListing([]byte(output))
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, vm := range targets {
		if vm.Fields["create"] == createdState {
			existing[vmKey(vm.OS, vm.Type, vm.Arch, vm.Name)] = true
		}
	}
	for key := range g.pendingInstances {
		existing[key] = true
	}
	if g.pendingInstances[target.Key()] {
		return nil, fmt.Errorf("VM %s is already being created", target.Key())
	}
	if !existing[target.Key()] && len(existing) >= g.cfg.MaxInstances {
		return nil, fmt.Errorf("VM instance limit reached (%d); destroy an unused instance or adjust max_instances on the host", g.cfg.MaxInstances)
	}
	if g.pendingInstances == nil {
		g.pendingInstances = map[string]bool{}
	}
	g.pendingInstances[target.Key()] = true
	return func() { g.instanceMu.Lock(); delete(g.pendingInstances, target.Key()); g.instanceMu.Unlock() }, nil
}
