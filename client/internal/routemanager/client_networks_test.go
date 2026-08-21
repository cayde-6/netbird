package routemanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/netbirdio/netbird/client/internal/routemanager/client"
	"github.com/netbirdio/netbird/route"
)

// TestUpdateClientNetworksDoesNotStopObsoleteClients pins down that updateClientNetworks no
// longer stops obsolete watchers itself - that responsibility moved to the caller (UpdateRoutes),
// which must now call stopObsoleteClients before updateSystemRoutes so a dynamic route's allowed
// IPs are released before its handler's state is wiped. This test only checks that
// updateClientNetworks leaves obsolete entries alone; it does NOT cover the ordering against
// updateSystemRoutes in UpdateRoutes itself.
func TestUpdateClientNetworksDoesNotStopObsoleteClients(t *testing.T) {
	id := route.HAUniqueID("net1||10.0.0.0/24")
	watcher := client.NewWatcher(client.WatcherConfig{Context: context.Background()})

	m := &DefaultManager{
		clientNetworks: map[route.HAUniqueID]*client.Watcher{
			id: watcher,
		},
		activeRoutes: map[route.HAUniqueID]client.RouteHandler{},
	}

	// networks no longer contains id, but updateClientNetworks must not touch it.
	m.updateClientNetworks(1, route.HAMap{})

	assert.Contains(t, m.clientNetworks, id, "updateClientNetworks must leave obsolete watchers for the caller to stop")

	m.stopObsoleteClients(route.HAMap{})

	assert.NotContains(t, m.clientNetworks, id, "stopObsoleteClients must remove the obsolete watcher")
}

// TestEnsureClientNetworkWatcherReusesExisting pins down that the shared watcher-start helper is
// idempotent: TriggerSelection and updateClientNetworks both route through it, and a second call
// for the same HA id must hand back the running watcher instead of starting a rival one that would
// take over the route's allowed IPs.
func TestEnsureClientNetworkWatcherReusesExisting(t *testing.T) {
	id := route.HAUniqueID("net1||10.0.0.0/24")
	existing := client.NewWatcher(client.WatcherConfig{Context: context.Background()})

	m := &DefaultManager{
		ctx:            context.Background(),
		clientNetworks: map[route.HAUniqueID]*client.Watcher{id: existing},
		activeRoutes:   map[route.HAUniqueID]client.RouteHandler{},
	}

	got := m.ensureClientNetworkWatcher(id, []*route.Route{{ID: "r1"}})

	assert.Same(t, existing, got, "must return the already running watcher")
	assert.Len(t, m.clientNetworks, 1, "must not register a second watcher for the same id")
}

// TestEnsureClientNetworkWatcherWithoutHandler covers the skip path: with no handler registered
// there is nothing to watch, so the helper must report that rather than start a watcher.
func TestEnsureClientNetworkWatcherWithoutHandler(t *testing.T) {
	m := &DefaultManager{
		ctx:            context.Background(),
		clientNetworks: map[route.HAUniqueID]*client.Watcher{},
		activeRoutes:   map[route.HAUniqueID]client.RouteHandler{},
	}

	got := m.ensureClientNetworkWatcher(route.HAUniqueID("net1||10.0.0.0/24"), []*route.Route{{ID: "r1"}})

	assert.Nil(t, got, "must return nil when no handler is registered")
	assert.Empty(t, m.clientNetworks, "must not register a watcher without a handler")
}
