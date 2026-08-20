package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/netbirdio/netbird/client/proto"
)

// skippedSetConfigRequestFields lists the SetConfigRequest fields that
// SetConfigRequestHasConfigOverrides and SetConfigRequestChangesConfig are
// not expected to cover, each with the reason it is exempt. Any field not
// in this list and not covered by setConfigRequestFieldCases below fails
// TestSetConfigRequestFieldParity, so a newly added proto field cannot
// silently slip past both functions the way that produced the bugs those
// functions exist to prevent.
var skippedSetConfigRequestFields = map[string]string{
	"Username":    "identifies which profile/user to target, not a config value",
	"ProfileName": "identifies which profile/user to target, not a config value",

	// LazyConnectionEnabled is accepted on the wire but setConfigInputFromRequest
	// (server.go) never copies it into the ConfigInput UpdateConfig applies, so it
	// correctly participates in neither the overrides-presence check nor the
	// value-change check: there's nothing for it to gate.
	"LazyConnectionEnabled": "not applied by setConfigInputFromRequest, so it has no effect to gate on",

	// protobuf-internal bookkeeping fields on the generated struct - not
	// wire fields, not config values, and unexported (unsettable via reflect).
	"state":         "protobuf-internal bookkeeping field, not a config value",
	"sizeCache":     "protobuf-internal bookkeeping field, not a config value",
	"unknownFields": "protobuf-internal bookkeeping field, not a config value",
}

// setConfigRequestFieldCase describes, for one covered SetConfigRequest
// field, a request that sets ONLY that field to a value that differs from
// the corresponding zero-value GetConfigResponse, plus an optional mutator
// to prepare `current` so the change is actually observable. The "clean
// list" flags (CleanNATExternalIPs, CleanDNSLabels, CleanSplitTunnel) need
// this: clearing an already-empty list is not a change, so current must
// carry a non-empty list for the request to count as one.
type setConfigRequestFieldCase struct {
	req        *proto.SetConfigRequest
	prepareCur func(*proto.GetConfigResponse)
}

func stringPtr(v string) *string { return &v }
func int64Ptr(v int64) *int64    { return &v }
func int32Ptr(v int32) *int32    { return &v }

// setConfigRequestFieldCases maps every SetConfigRequest field NOT in
// skippedSetConfigRequestFields to the case that exercises it. Building
// this map is the "small explicit table" the clean-list flags need,
// per the task's requirement to avoid hardcoding those by index.
var setConfigRequestFieldCases = map[string]setConfigRequestFieldCase{
	"ManagementUrl": {req: &proto.SetConfigRequest{ManagementUrl: "https://example.com"}},
	"AdminURL":      {req: &proto.SetConfigRequest{AdminURL: "https://admin.example.com"}},

	"RosenpassEnabled":              {req: &proto.SetConfigRequest{RosenpassEnabled: boolPtr(true)}},
	"InterfaceName":                 {req: &proto.SetConfigRequest{InterfaceName: stringPtr("wt1")}},
	"WireguardPort":                 {req: &proto.SetConfigRequest{WireguardPort: int64Ptr(51821)}},
	"OptionalPreSharedKey":          {req: &proto.SetConfigRequest{OptionalPreSharedKey: stringPtr("new-psk")}},
	"DisableAutoConnect":            {req: &proto.SetConfigRequest{DisableAutoConnect: boolPtr(true)}},
	"ServerSSHAllowed":              {req: &proto.SetConfigRequest{ServerSSHAllowed: boolPtr(true)}},
	"RosenpassPermissive":           {req: &proto.SetConfigRequest{RosenpassPermissive: boolPtr(true)}},
	"NetworkMonitor":                {req: &proto.SetConfigRequest{NetworkMonitor: boolPtr(true)}},
	"DisableClientRoutes":           {req: &proto.SetConfigRequest{DisableClientRoutes: boolPtr(true)}},
	"DisableServerRoutes":           {req: &proto.SetConfigRequest{DisableServerRoutes: boolPtr(true)}},
	"DisableDns":                    {req: &proto.SetConfigRequest{DisableDns: boolPtr(true)}},
	"DisableFirewall":               {req: &proto.SetConfigRequest{DisableFirewall: boolPtr(true)}},
	"BlockLanAccess":                {req: &proto.SetConfigRequest{BlockLanAccess: boolPtr(true)}},
	"DisableNotifications":          {req: &proto.SetConfigRequest{DisableNotifications: boolPtr(true)}},
	"BlockInbound":                  {req: &proto.SetConfigRequest{BlockInbound: boolPtr(true)}},
	"EnableSSHRoot":                 {req: &proto.SetConfigRequest{EnableSSHRoot: boolPtr(true)}},
	"EnableSSHSFTP":                 {req: &proto.SetConfigRequest{EnableSSHSFTP: boolPtr(true)}},
	"EnableSSHLocalPortForwarding":  {req: &proto.SetConfigRequest{EnableSSHLocalPortForwarding: boolPtr(true)}},
	"EnableSSHRemotePortForwarding": {req: &proto.SetConfigRequest{EnableSSHRemotePortForwarding: boolPtr(true)}},
	"DisableSSHAuth":                {req: &proto.SetConfigRequest{DisableSSHAuth: boolPtr(true)}},
	"SshJWTCacheTTL":                {req: &proto.SetConfigRequest{SshJWTCacheTTL: int32Ptr(100)}},
	"DisableIpv6":                   {req: &proto.SetConfigRequest{DisableIpv6: boolPtr(true)}},
	"Mtu":                           {req: &proto.SetConfigRequest{Mtu: int64Ptr(1300)}},

	"NatExternalIPs": {req: &proto.SetConfigRequest{NatExternalIPs: []string{"1.2.3.4"}}},
	"CleanNATExternalIPs": {
		req: &proto.SetConfigRequest{CleanNATExternalIPs: true},
		prepareCur: func(cur *proto.GetConfigResponse) {
			cur.NatExternalIPs = []string{"1.2.3.4"}
		},
	},

	"CustomDNSAddress":    {req: &proto.SetConfigRequest{CustomDNSAddress: []byte("127.0.0.1:53")}},
	"ExtraIFaceBlacklist": {req: &proto.SetConfigRequest{ExtraIFaceBlacklist: []string{"custom0"}}},

	"DnsLabels": {req: &proto.SetConfigRequest{DnsLabels: []string{"vpc1"}}},
	"CleanDNSLabels": {
		req: &proto.SetConfigRequest{CleanDNSLabels: true},
		prepareCur: func(cur *proto.GetConfigResponse) {
			cur.DnsLabels = []string{"vpc1"}
		},
	},

	"DnsRouteInterval": {req: &proto.SetConfigRequest{DnsRouteInterval: durationpb.New(5 * time.Minute)}},

	"SplitTunnel": {req: &proto.SetConfigRequest{SplitTunnel: []string{"example.com"}}},
	"CleanSplitTunnel": {
		req: &proto.SetConfigRequest{CleanSplitTunnel: true},
		prepareCur: func(cur *proto.GetConfigResponse) {
			cur.SplitTunnel = []string{"example.com"}
		},
	},
}

// TestSetConfigRequestFieldParity guards against the class of bug this
// package's value-aware reapply logic exists to prevent: a new
// SetConfigRequest field added to the proto but forgotten in
// SetConfigRequestHasConfigOverrides and/or SetConfigRequestChangesConfig
// silently loses whatever flag it represents (present in the request,
// but treated as "no override" / "no change"). Reflection over the
// generated struct means the test fails the moment such a field is
// added, rather than only when someone remembers to update it by hand.
func TestSetConfigRequestFieldParity(t *testing.T) {
	reqType := reflect.TypeOf(proto.SetConfigRequest{})

	for i := 0; i < reqType.NumField(); i++ {
		f := reqType.Field(i)
		name := f.Name

		if reason, skipped := skippedSetConfigRequestFields[name]; skipped {
			t.Logf("skipping field %s: %s", name, reason)
			continue
		}

		tc, ok := setConfigRequestFieldCases[name]
		if !ok {
			t.Fatalf("SetConfigRequest field %q is not covered by setConfigRequestFieldCases and not "+
				"listed in skippedSetConfigRequestFields; add a case for it (or a documented skip) and make "+
				"sure SetConfigRequestHasConfigOverrides and SetConfigRequestChangesConfig actually apply it, "+
				"or a future `netbird up --%s=...` may silently do nothing", name, name)
		}

		t.Run(name, func(t *testing.T) {
			require.True(t, SetConfigRequestHasConfigOverrides(tc.req),
				"SetConfigRequestHasConfigOverrides must report an override for field %s", name)

			current := &proto.GetConfigResponse{FullConfigSnapshot: true}
			if tc.prepareCur != nil {
				tc.prepareCur(current)
			}

			require.True(t, SetConfigRequestChangesConfig(tc.req, current),
				"SetConfigRequestChangesConfig must report a change for field %s", name)
		})
	}
}
