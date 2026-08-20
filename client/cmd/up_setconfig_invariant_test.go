package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/client/proto"
	"github.com/netbirdio/netbird/client/server"
)

// snapshotFlagsChanged records the Changed state of every flag on cmd
// (persistent + local) and returns a func that restores it. Used to
// insulate this test from any other test that has already parsed flags
// on the shared, package-level upCmd/rootCmd instances (and vice versa).
func snapshotFlagsChanged(t *testing.T, fs *pflag.FlagSet) func() {
	t.Helper()
	changed := map[string]bool{}
	fs.VisitAll(func(f *pflag.Flag) {
		changed[f.Name] = f.Changed
	})
	return func() {
		fs.VisitAll(func(f *pflag.Flag) {
			f.Changed = changed[f.Name]
		})
	}
}

// TestSetupSetConfigReq_BareUp_NotAChange is the central safety net for the
// reapply-on-value-change machinery: a plain `netbird up`, with every flag
// left at its default and none of them Changed, must not be treated as a
// configuration change. Regressing this either makes `netbird up` restart
// an already-connected tunnel for no reason, or (via
// SetConfigRequestHasConfigOverrides's presence-only fallback) does the
// same for any flag env-var-populated by SetFlagsFromEnvVars.
func TestSetupSetConfigReq_BareUp_NotAChange(t *testing.T) {
	restoreUpCmd := snapshotFlagsChanged(t, upCmd.PersistentFlags())
	restoreRootCmd := snapshotFlagsChanged(t, rootCmd.PersistentFlags())
	t.Cleanup(restoreUpCmd)
	t.Cleanup(restoreRootCmd)

	// upCmd registers all of its flags via PersistentFlags(), not Flags():
	// Flags() only gets populated by cobra's mergePersistentFlags(), which
	// runs during Execute()/ParseFlags(), so before that it is empty and
	// every Lookup on it silently returns nil. Assert the flags are found
	// on the FlagSet this test actually operates on, so a future refactor
	// back to Flags() fails loudly here instead of quietly turning the
	// rest of this test into a no-op.
	require.NotNil(t, upCmd.PersistentFlags().Lookup(mtuFlag), "sanity check: mtu flag must be registered on upCmd.PersistentFlags()")
	require.NotNil(t, upCmd.PersistentFlags().Lookup(splitTunnelFlag), "sanity check: split-tunnel flag must be registered on upCmd.PersistentFlags()")

	// Force every flag setupSetConfigReq looks at back to "not changed",
	// exactly the state a freshly-started `netbird up` process has before
	// cobra parses any args.
	upCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })

	// setupSetConfigReq copies these package-level vars into the request
	// unconditionally (not gated by a flag's Changed state), so a value left
	// behind by an unrelated test that binds the same package var to its own
	// throwaway *cobra.Command (e.g. TestSetFlagsFromEnvVars, which leaves
	// natExternalIPs populated) would otherwise leak into this test. Reset
	// them to the zero value a freshly-started process has, and restore
	// whatever was there afterwards.
	origManagementURL, origAdminURL := managementURL, adminURL
	origNatExternalIPs, origExtraIFaceBlackList := natExternalIPs, extraIFaceBlackList
	origDNSLabels, origDNSLabelsValidated, origSplitTunnel := dnsLabels, dnsLabelsValidated, splitTunnel
	t.Cleanup(func() {
		managementURL, adminURL = origManagementURL, origAdminURL
		natExternalIPs, extraIFaceBlackList = origNatExternalIPs, origExtraIFaceBlackList
		dnsLabels, dnsLabelsValidated, splitTunnel = origDNSLabels, origDNSLabelsValidated, origSplitTunnel
	})
	managementURL, adminURL = "", ""
	natExternalIPs, extraIFaceBlackList = nil, nil
	dnsLabels, dnsLabelsValidated, splitTunnel = nil, nil, nil

	req, err := setupSetConfigReq(nil, upCmd, "test-profile", "test-user")
	require.NoError(t, err)
	require.NotNil(t, req)

	require.False(t, server.SetConfigRequestHasConfigOverrides(req),
		"a bare `netbird up` request must not carry any config override")

	// current == nil (daemon config unreadable): must still fall through to
	// the presence-only check above and report no change.
	require.False(t, server.SetConfigRequestChangesConfig(req, nil))

	// A populated current config must not flip the result either: none of
	// the request's fields participate in the comparison when unset.
	current := &proto.GetConfigResponse{
		ManagementUrl:                 "https://api.netbird.io:443",
		AdminURL:                      "https://app.netbird.io:443",
		InterfaceName:                 "wt0",
		WireguardPort:                 51820,
		Mtu:                           1280,
		DisableAutoConnect:            false,
		ServerSSHAllowed:              false,
		RosenpassEnabled:              false,
		RosenpassPermissive:           false,
		DisableNotifications:          true,
		NetworkMonitor:                false,
		BlockInbound:                  false,
		DisableDns:                    false,
		DisableClientRoutes:           false,
		DisableServerRoutes:           false,
		BlockLanAccess:                false,
		EnableSSHRoot:                 false,
		EnableSSHSFTP:                 false,
		EnableSSHLocalPortForwarding:  false,
		EnableSSHRemotePortForwarding: false,
		DisableSSHAuth:                false,
		SshJWTCacheTTL:                0,
		DisableIpv6:                   false,
		DisableFirewall:               false,
		SplitTunnel:                   []string{"example.com"},
		DnsLabels:                     []string{"vpc1"},
		NatExternalIPs:                []string{"1.2.3.4"},
		IfaceBlacklist:                []string{"eth1"},
		CustomDNSAddress:              []byte("127.0.0.1:53"),
		FullConfigSnapshot:            true,
	}
	require.False(t, server.SetConfigRequestChangesConfig(req, current))
}
