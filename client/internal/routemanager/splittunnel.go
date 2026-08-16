package routemanager

import (
	"fmt"
	"net/netip"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/route"
	"github.com/netbirdio/netbird/shared/management/domain"
)

// Spec is the parsed split tunnel configuration: the domains and prefixes that
// should be routed through the tunnel while everything else goes out directly.
type Spec struct {
	Domains  domain.List
	Prefixes []netip.Prefix
}

// IsEmpty reports whether the spec routes nothing. An empty spec leaves routes
// untouched, which keeps the full tunnel behaviour.
func (s Spec) IsEmpty() bool {
	return len(s.Domains) == 0 && len(s.Prefixes) == 0
}

// ParseSpec splits configured entries into prefixes and domains. An entry that
// parses as a prefix or a bare address is a prefix, everything else has to be a
// valid hostname. Rejecting garbage here matters: a typo would otherwise become
// a domain route that never resolves, visible only as a repeating resolver
// failure in the daemon log.
func ParseSpec(entries []string) (Spec, error) {
	var spec Spec

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(entry); err == nil {
			spec.Prefixes = append(spec.Prefixes, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			spec.Prefixes = append(spec.Prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}

		d, err := domain.FromString(entry)
		if err != nil {
			return Spec{}, fmt.Errorf("parse split tunnel entry %q: %w", entry, err)
		}
		// FromString only runs the IDNA conversion, which lets plenty of
		// non-hostnames through. Wildcards are allowed, the domain package
		// supports them and the resolver handles them.
		if !domain.IsValidDomain(d.PunycodeString()) {
			return Spec{}, fmt.Errorf("split tunnel entry %q is neither a valid prefix nor a valid domain", entry)
		}

		spec.Domains = append(spec.Domains, d)
	}

	return spec, nil
}

// Transform rewrites the default route into a domain route carrying the split
// tunnel domains and adds a route per configured prefix. Non-default routes pass
// through unchanged, the IPv6 default is dropped so IPv6 stays direct.
//
// It must run after the route selector has filtered exit nodes: a rewritten
// route no longer looks like an exit node to the selector, so transforming
// earlier would apply every offered exit node at once.
func Transform(routes route.HAMap, spec Spec) route.HAMap {
	if spec.IsEmpty() {
		return routes
	}

	result := route.HAMap{}
	add := func(r *route.Route) {
		id := r.GetHAUniqueID()
		result[id] = append(result[id], r)
	}

	var sawDefault bool
	for _, group := range routes {
		for _, r := range group {
			if route.IsV6DefaultRoute(r.Network) {
				// Domains are already covered by the rewritten v4 route and the
				// whole v6 space must not be tunneled.
				continue
			} else if route.IsV4DefaultRoute(r.Network) {
				sawDefault = true
				for _, rewritten := range rewriteDefault(r, spec) {
					add(rewritten)
				}
			} else {
				add(r)
			}
		}
	}

	if !sawDefault {
		// Nothing to rewrite, and no peer to attach a synthetic route to. Pass
		// the routes through so the connection keeps working, but say so: this
		// is the quiet way split tunneling ends up doing nothing at all.
		log.Warnf("split tunneling is configured but the server offered no default route (0.0.0.0/0); " +
			"no split tunnel routes were installed — most likely no exit node is selected")
	}

	return result
}

// rewriteDefault turns a default route into the concrete routes the spec asks
// for, keeping the peer so HA failover keeps working.
func rewriteDefault(r *route.Route, spec Spec) []*route.Route {
	var out []*route.Route

	if len(spec.Domains) > 0 {
		domainRoute := *r
		domainRoute.Network = netip.Prefix{}
		domainRoute.Domains = spec.Domains
		domainRoute.NetworkType = route.DomainNetwork
		// Keep resolved addresses instead of dropping them, so a CDN swapping
		// addresses cannot break access between resolver ticks.
		domainRoute.KeepRoute = true
		out = append(out, &domainRoute)
	}

	for i, prefix := range spec.Prefixes {
		prefixRoute := *r
		prefixRoute.Network = prefix
		prefixRoute.Domains = nil
		prefixRoute.NetworkType = route.IPv4Network
		if prefix.Addr().Is6() {
			prefixRoute.NetworkType = route.IPv6Network
		}
		prefixRoute.ID = route.ID(fmt.Sprintf("%s-st%d", r.ID, i))
		prefixRoute.NetID = route.NetID(fmt.Sprintf("%s-st%d", r.NetID, i))
		out = append(out, &prefixRoute)
	}

	return out
}
