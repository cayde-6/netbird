package routemanager

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/client/internal/routemanager/vars"
	"github.com/netbirdio/netbird/route"
	"github.com/netbirdio/netbird/shared/management/domain"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name         string
		entries      []string
		wantDomains  domain.List
		wantPrefixes []netip.Prefix
		wantErr      bool
	}{
		{
			name:        "domains only",
			entries:     []string{"gitlab.example.com", "jira.example.com"},
			wantDomains: domain.List{"gitlab.example.com", "jira.example.com"},
		},
		{
			name:         "prefix only",
			entries:      []string{"10.50.0.0/16"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
		},
		{
			name:         "bare ip becomes host prefix",
			entries:      []string{"192.0.2.10"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")},
		},
		{
			name:         "mixed entries",
			entries:      []string{"gitlab.example.com", "10.50.0.0/16"},
			wantDomains:  domain.List{"gitlab.example.com"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
		},
		{
			name:    "empty and blank entries are skipped",
			entries: []string{"", "   "},
		},
		{
			name:    "nil entries",
			entries: nil,
		},
		{
			name:        "single label hostname is a valid domain",
			entries:     []string{"intranet"},
			wantDomains: domain.List{"intranet"},
		},
		{
			name:        "wildcard domain",
			entries:     []string{"*.example.com"},
			wantDomains: domain.List{"*.example.com"},
		},
		{
			name:    "entry with whitespace inside",
			entries: []string{"not a domain"},
			wantErr: true,
		},
		{
			name:    "entry with punctuation",
			entries: []string{"not-a-domain!!"},
			wantErr: true,
		},
		{
			name:    "empty label",
			entries: []string{"a..b"},
			wantErr: true,
		},
		{
			name:    "leading dot",
			entries: []string{".example.com"},
			wantErr: true,
		},
		{
			name:    "trailing dot",
			entries: []string{"example.com."},
			wantErr: true,
		},
		{
			name:    "label starting with a hyphen",
			entries: []string{"-bad-.example"},
			wantErr: true,
		},
		{
			name:    "label ending with a hyphen",
			entries: []string{"bad-.example"},
			wantErr: true,
		},
		{
			name:    "a valid entry does not rescue an invalid one",
			entries: []string{"gitlab.example.com", "a..b"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ParseSpec(tc.entries)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantDomains, spec.Domains)
			require.Equal(t, tc.wantPrefixes, spec.Prefixes)
		})
	}
}

func TestParseSpecNormalizesDomainCase(t *testing.T) {
	spec, err := ParseSpec([]string{"GitLab.Example.Com"})

	require.NoError(t, err)
	require.Equal(t, domain.List{"gitlab.example.com"}, spec.Domains)
}

func TestSpecIsEmpty(t *testing.T) {
	require.True(t, Spec{}.IsEmpty())
	require.False(t, Spec{Domains: domain.List{"example.com"}}.IsEmpty())
	require.False(t, Spec{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}.IsEmpty())
}

func haMap(routes ...*route.Route) route.HAMap {
	m := route.HAMap{}
	for _, r := range routes {
		m[r.GetHAUniqueID()] = append(m[r.GetHAUniqueID()], r)
	}
	return m
}

func defaultRoute(id, netID, peer string) *route.Route {
	return &route.Route{
		ID:          route.ID(id),
		NetID:       route.NetID(netID),
		Peer:        peer,
		Network:     vars.Defaultv4,
		NetworkType: route.IPv4Network,
	}
}

func TestTransformRewritesDefaultRoute(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(defaultRoute("route-1", "Auto (Load Balancer)", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 1)
	var got []*route.Route
	for _, rs := range out {
		got = rs
	}
	require.Len(t, got, 1)

	r := got[0]
	require.Equal(t, domain.List{"gitlab.example.com"}, r.Domains)
	require.Equal(t, route.DomainNetwork, r.NetworkType)
	require.False(t, r.Network.IsValid(), "network must be cleared on a domain route")
	require.True(t, r.KeepRoute, "KeepRoute keeps CDN addresses from being dropped")
	require.Equal(t, "peer-tr", r.Peer)
	require.Equal(t, route.NetID("Auto (Load Balancer)"), r.NetID)
	require.Equal(t, route.ID("route-1"), r.ID)
	require.True(t, r.IsDynamic())
}

func TestTransformKeyedByRewrittenHAID(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	for key, rs := range out {
		require.Equal(t, rs[0].GetHAUniqueID(), key, "map key must match the rewritten route")
	}
}

func TestTransformPreservesHAGroup(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(
		defaultRoute("route-1", "Auto", "peer-tr"),
		defaultRoute("route-2", "Auto", "peer-ned"),
	)

	out := Transform(in, spec)

	require.Len(t, out, 1, "both routes share one HA group")
	var peers []string
	for _, rs := range out {
		for _, r := range rs {
			peers = append(peers, r.Peer)
		}
	}
	require.ElementsMatch(t, []string{"peer-tr", "peer-ned"}, peers)
}

func TestTransformPassesThroughNonDefaultRoutes(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	internal := &route.Route{
		ID:          "route-lan",
		NetID:       "lan",
		Peer:        "peer-tr",
		Network:     netip.MustParsePrefix("10.0.0.0/22"),
		NetworkType: route.IPv4Network,
	}
	in := haMap(internal)

	out := Transform(in, spec)

	require.Len(t, out, 1)
	require.Equal(t, internal, out[internal.GetHAUniqueID()][0])
}

func TestTransformDropsDefaultV6Route(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	v6 := &route.Route{
		ID:          "route-v6",
		NetID:       "Auto",
		Peer:        "peer-tr",
		Network:     vars.Defaultv6,
		NetworkType: route.IPv6Network,
	}
	in := haMap(v6)

	out := Transform(in, spec)

	require.Empty(t, out, "IPv6 default must not be tunneled")
}

func TestTransformEmptySpecIsNoop(t *testing.T) {
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, Spec{})

	require.Equal(t, in, out)
}

func TestTransformAddsPrefixRoutes(t *testing.T) {
	spec := Spec{
		Domains:  domain.List{"gitlab.example.com"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
	}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 2, "one domain route plus one prefix route")

	var prefixRoute *route.Route
	for _, rs := range out {
		for _, r := range rs {
			if !r.IsDynamic() {
				prefixRoute = r
			}
		}
	}
	require.NotNil(t, prefixRoute)
	require.Equal(t, netip.MustParsePrefix("10.50.0.0/16"), prefixRoute.Network)
	require.Equal(t, route.IPv4Network, prefixRoute.NetworkType)
	require.Equal(t, "peer-tr", prefixRoute.Peer)
	require.NotEqual(t, route.ID("route-1"), prefixRoute.ID, "prefix route needs its own ID")
}

// TestTransformNeverEmitsDefaultRoute guards the invariant the whole feature
// rests on: with a non-empty spec no default route may reach the system, or
// every packet on the machine goes into the tunnel.
func TestTransformNeverEmitsDefaultRoute(t *testing.T) {
	spec := Spec{
		Domains:  domain.List{"gitlab.example.com"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
	}
	in := haMap(
		defaultRoute("route-1", "Auto", "peer-tr"),
		defaultRoute("route-2", "Auto", "peer-ned"),
		defaultRoute("route-3", "exit-ams", "peer-ams"),
		&route.Route{
			ID:          "route-v6",
			NetID:       "Auto",
			Peer:        "peer-tr",
			Network:     vars.Defaultv6,
			NetworkType: route.IPv6Network,
		},
		&route.Route{
			ID:          "route-lan",
			NetID:       "lan",
			Peer:        "peer-tr",
			Network:     netip.MustParsePrefix("10.0.0.0/22"),
			NetworkType: route.IPv4Network,
		},
	)

	out := Transform(in, spec)

	require.NotEmpty(t, out)
	for _, rs := range out {
		for _, r := range rs {
			require.NotEqual(t, vars.Defaultv4, r.Network, "route %s still carries the v4 default", r.ID)
			require.NotEqual(t, vars.Defaultv6, r.Network, "route %s still carries the v6 default", r.ID)
		}
	}
}

func TestTransformEmptyInput(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}

	require.Empty(t, Transform(route.HAMap{}, spec))
	require.Empty(t, Transform(nil, spec))
}

func TestTransformPrefixOnlySpec(t *testing.T) {
	spec := Spec{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")}}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 1)
	for _, rs := range out {
		require.False(t, rs[0].IsDynamic())
		require.Equal(t, netip.MustParsePrefix("10.50.0.0/16"), rs[0].Network)
	}
}
