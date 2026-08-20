package server

import (
	"bytes"
	"slices"

	"github.com/netbirdio/netbird/client/proto"
)

// SetConfigRequestChangesConfig reports whether applying msg to the
// currently stored configuration described by current would actually
// change anything. It is the value-aware counterpart to
// SetConfigRequestHasConfigOverrides: that function only checks whether a
// field was set in the request, which trips on env-var-populated flags
// (SetFlagsFromEnvVars in cmd/root.go marks them Changed=true even when
// their value matches what's already persisted) and would otherwise make
// `netbird up` reconnect on every invocation in container/scripted
// deployments. This function compares the desired value against the
// daemon's current GetConfig snapshot and only reports a change when the
// value actually diverges.
func SetConfigRequestChangesConfig(msg *proto.SetConfigRequest, current *proto.GetConfigResponse) bool {
	if msg == nil {
		return false
	}

	if current == nil || !current.FullConfigSnapshot {
		// Could not read the current config (e.g. GetConfig RPC failed), or
		// read it from a daemon that predates FullConfigSnapshot (fields
		// 29-35 absent, decoded as their zero value). Either way, an
		// incomplete snapshot cannot be used for a value-by-value
		// comparison: an absent field is indistinguishable from one that is
		// genuinely zero, so comparing against it would report "unchanged"
		// for a flag whose value actually differs, and the CLI would then
		// silently drop it. Fall back to the presence-only check instead:
		// the safe direction is to reconnect when the request carries any
		// override (an extra reconnect is harmless) and to do nothing when
		// it carries none - never to silently lose a flag.
		return SetConfigRequestHasConfigOverrides(msg)
	}

	// Normalize both sides with canonicalURL (mdm.go) before comparing: msg
	// carries whatever the user typed (e.g. "https://netbird.example.com",
	// no explicit port), while current comes from the config layer, which
	// always appends the scheme's default port (":443"/":80"). Comparing
	// the raw strings would report a spurious change - and therefore an
	// unwanted reconnect - on every `netbird up` for a management/admin URL
	// that was never actually changed. canonicalURL is also what
	// conflictURL uses for the equivalent MDM comparison.
	if msg.ManagementUrl != "" && canonicalURL(msg.ManagementUrl) != canonicalURL(current.ManagementUrl) {
		return true
	}
	if msg.AdminURL != "" && canonicalURL(msg.AdminURL) != canonicalURL(current.AdminURL) {
		return true
	}

	// PreSharedKey: GetConfig always returns the PSK masked as
	// "**********" (see preSharedKeyRedactedSentinel in mdm.go), so there
	// is no way to compare the requested value against the stored one.
	// Treat any explicit override as a change; this only costs an extra
	// reconnect on an exact-value no-op PSK re-submission, which is rare
	// and safe.
	if msg.OptionalPreSharedKey != nil {
		return true
	}

	if len(msg.CustomDNSAddress) > 0 {
		desired := msg.CustomDNSAddress
		// "empty" is the sentinel parseCustomDNSAddress (cmd/up.go) uses
		// to mean "clear the custom DNS address"; setConfigInputFromRequest
		// (server.go) resolves it to an empty stored value the same way.
		if string(msg.CustomDNSAddress) == "empty" {
			desired = nil
		}
		if !bytes.Equal(desired, current.CustomDNSAddress) {
			return true
		}
	}

	if changed := slicePlusCleanChanged(msg.NatExternalIPs, msg.CleanNATExternalIPs, current.NatExternalIPs); changed {
		return true
	}
	if changed := slicePlusCleanChanged(msg.DnsLabels, msg.CleanDNSLabels, current.DnsLabels); changed {
		return true
	}
	if changed := slicePlusCleanChanged(msg.SplitTunnel, msg.CleanSplitTunnel, current.SplitTunnel); changed {
		return true
	}

	if extraIFaceBlacklistChanged(msg.ExtraIFaceBlacklist, current.IfaceBlacklist) {
		return true
	}

	if msg.DnsRouteInterval != nil && msg.DnsRouteInterval.AsDuration() != current.DnsRouteInterval.AsDuration() {
		return true
	}

	if msg.InterfaceName != nil && *msg.InterfaceName != current.InterfaceName {
		return true
	}
	if msg.WireguardPort != nil && *msg.WireguardPort != current.WireguardPort {
		return true
	}
	if msg.Mtu != nil && *msg.Mtu != current.Mtu {
		return true
	}
	if msg.SshJWTCacheTTL != nil && *msg.SshJWTCacheTTL != current.SshJWTCacheTTL {
		return true
	}

	return boolPtrChanged(msg.RosenpassEnabled, current.RosenpassEnabled) ||
		boolPtrChanged(msg.RosenpassPermissive, current.RosenpassPermissive) ||
		boolPtrChanged(msg.DisableAutoConnect, current.DisableAutoConnect) ||
		boolPtrChanged(msg.ServerSSHAllowed, current.ServerSSHAllowed) ||
		boolPtrChanged(msg.NetworkMonitor, current.NetworkMonitor) ||
		boolPtrChanged(msg.DisableClientRoutes, current.DisableClientRoutes) ||
		boolPtrChanged(msg.DisableServerRoutes, current.DisableServerRoutes) ||
		boolPtrChanged(msg.DisableDns, current.DisableDns) ||
		boolPtrChanged(msg.DisableFirewall, current.DisableFirewall) ||
		boolPtrChanged(msg.BlockLanAccess, current.BlockLanAccess) ||
		boolPtrChanged(msg.DisableNotifications, current.DisableNotifications) ||
		boolPtrChanged(msg.BlockInbound, current.BlockInbound) ||
		boolPtrChanged(msg.DisableIpv6, current.DisableIpv6) ||
		boolPtrChanged(msg.EnableSSHRoot, current.EnableSSHRoot) ||
		boolPtrChanged(msg.EnableSSHSFTP, current.EnableSSHSFTP) ||
		boolPtrChanged(msg.EnableSSHLocalPortForwarding, current.EnableSSHLocalPortForwarding) ||
		boolPtrChanged(msg.EnableSSHRemotePortForwarding, current.EnableSSHRemotePortForwarding) ||
		boolPtrChanged(msg.DisableSSHAuth, current.DisableSSHAuth)
}

// boolPtrChanged reports whether an optional bool field in the request
// diverges from the corresponding current value. A nil pointer means the
// field was not requested, so it never counts as a change.
func boolPtrChanged(want *bool, got bool) bool {
	return want != nil && *want != got
}

// slicePlusCleanChanged implements the "slice + clean flag" comparison
// shared by NatExternalIPs/CleanNATExternalIPs, DnsLabels/CleanDNSLabels
// and SplitTunnel/CleanSplitTunnel: the field participates in the
// comparison when the desired slice is non-empty or the clean flag is
// set (clean means "desired value is the empty list"). Element order
// matters: a reordering is reported as a change, which only costs an
// extra (harmless) reconnect.
func slicePlusCleanChanged(desired []string, clean bool, got []string) bool {
	if len(desired) == 0 && !clean {
		return false
	}
	if clean {
		desired = nil
	}
	return !slices.Equal(desired, got)
}

// extraIFaceBlacklistChanged reports whether applying msg.ExtraIFaceBlacklist
// would change the daemon's effective interface blacklist. This is a subset
// comparison, not equality: the request carries only the entries to add,
// while GetConfigResponse.IfaceBlacklist is the effective list (built-in
// defaults plus everything already added). UpdateConfig only appends the
// entries from the request that are not already present, so the request
// changes anything precisely when at least one of its entries is missing
// from the current effective list. An empty request never participates,
// mirroring the other slice comparisons above.
func extraIFaceBlacklistChanged(desired []string, got []string) bool {
	for _, entry := range desired {
		if !slices.Contains(got, entry) {
			return true
		}
	}
	return false
}
