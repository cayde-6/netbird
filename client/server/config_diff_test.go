package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/netbirdio/netbird/client/proto"
)

func TestSetConfigRequestChangesConfig(t *testing.T) {
	psk := "some-psk"
	falseVal := false
	trueVal := true
	var mtu int64 = 1280
	var otherMtu int64 = 1300

	tests := []struct {
		name    string
		req     *proto.SetConfigRequest
		current *proto.GetConfigResponse
		want    bool
	}{
		{
			name:    "nil request",
			req:     nil,
			current: &proto.GetConfigResponse{},
			want:    false,
		},
		{
			name:    "nil current, no overrides",
			req:     &proto.SetConfigRequest{},
			current: nil,
			want:    false,
		},
		{
			name:    "nil current, with overrides",
			req:     &proto.SetConfigRequest{Mtu: &mtu},
			current: nil,
			want:    true,
		},
		{
			name:    "management url same as current",
			req:     &proto.SetConfigRequest{ManagementUrl: "https://api.netbird.io:443"},
			current: &proto.GetConfigResponse{ManagementUrl: "https://api.netbird.io:443", FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "management url differs from current",
			req:     &proto.SetConfigRequest{ManagementUrl: "https://other.example.com:443"},
			current: &proto.GetConfigResponse{ManagementUrl: "https://api.netbird.io:443", FullConfigSnapshot: true},
			want:    true,
		},
		{
			// The config layer normalizes a stored URL by appending the
			// scheme's default port (see canonicalURL in mdm.go); the user
			// rarely types one. Comparing raw strings would report this as
			// "changed" on every `netbird up`, forcing a needless reconnect.
			name:    "management url without port matches current with default https port",
			req:     &proto.SetConfigRequest{ManagementUrl: "https://netbird.example.com"},
			current: &proto.GetConfigResponse{ManagementUrl: "https://netbird.example.com:443", FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "management url without port matches current with default http port",
			req:     &proto.SetConfigRequest{ManagementUrl: "http://netbird.example.com"},
			current: &proto.GetConfigResponse{ManagementUrl: "http://netbird.example.com:80", FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "management url genuinely different host is a change",
			req:     &proto.SetConfigRequest{ManagementUrl: "https://netbird.example.com"},
			current: &proto.GetConfigResponse{ManagementUrl: "https://other.example.com:443", FullConfigSnapshot: true},
			want:    true,
		},
		{
			name:    "admin url differs from current",
			req:     &proto.SetConfigRequest{AdminURL: "https://other.example.com:443"},
			current: &proto.GetConfigResponse{AdminURL: "https://app.netbird.io:443", FullConfigSnapshot: true},
			want:    true,
		},
		{
			name:    "admin url not set is not a change",
			req:     &proto.SetConfigRequest{},
			current: &proto.GetConfigResponse{AdminURL: "https://app.netbird.io:443", FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "admin url without port matches current with default https port",
			req:     &proto.SetConfigRequest{AdminURL: "https://app.netbird.io"},
			current: &proto.GetConfigResponse{AdminURL: "https://app.netbird.io:443", FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "PSK always counts as a change, GetConfig masks it",
			req:     &proto.SetConfigRequest{OptionalPreSharedKey: &psk},
			current: &proto.GetConfigResponse{PreSharedKey: "**********", FullConfigSnapshot: true},
			want:    true,
		},
		{
			name: "split tunnel same content as current",
			req: &proto.SetConfigRequest{
				SplitTunnel: []string{"example.com", "10.0.0.0/8"},
			},
			current: &proto.GetConfigResponse{
				SplitTunnel:        []string{"example.com", "10.0.0.0/8"},
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			name: "split tunnel differs from current",
			req: &proto.SetConfigRequest{
				SplitTunnel: []string{"example.com"},
			},
			current: &proto.GetConfigResponse{
				SplitTunnel:        []string{"other.com"},
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "clean split tunnel while current is non-empty is a change",
			req: &proto.SetConfigRequest{
				CleanSplitTunnel: true,
			},
			current: &proto.GetConfigResponse{
				SplitTunnel:        []string{"example.com"},
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "clean split tunnel while current is already empty is not a change",
			req: &proto.SetConfigRequest{
				CleanSplitTunnel: true,
			},
			current: &proto.GetConfigResponse{
				SplitTunnel:        nil,
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			name: "custom dns address sentinel clears a non-empty current value",
			req: &proto.SetConfigRequest{
				CustomDNSAddress: []byte("empty"),
			},
			current: &proto.GetConfigResponse{
				CustomDNSAddress:   []byte("127.0.0.1:53"),
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "custom dns address sentinel with already-empty current value is not a change",
			req: &proto.SetConfigRequest{
				CustomDNSAddress: []byte("empty"),
			},
			current: &proto.GetConfigResponse{
				CustomDNSAddress:   nil,
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			name:    "optional bool same as current",
			req:     &proto.SetConfigRequest{RosenpassEnabled: &falseVal},
			current: &proto.GetConfigResponse{RosenpassEnabled: false, FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "optional bool differs from current",
			req:     &proto.SetConfigRequest{RosenpassEnabled: &trueVal},
			current: &proto.GetConfigResponse{RosenpassEnabled: false, FullConfigSnapshot: true},
			want:    true,
		},
		{
			name:    "mtu same as current",
			req:     &proto.SetConfigRequest{Mtu: &mtu},
			current: &proto.GetConfigResponse{Mtu: mtu, FullConfigSnapshot: true},
			want:    false,
		},
		{
			name:    "mtu differs from current",
			req:     &proto.SetConfigRequest{Mtu: &mtu},
			current: &proto.GetConfigResponse{Mtu: otherMtu, FullConfigSnapshot: true},
			want:    true,
		},
		{
			name: "dns route interval same as current",
			req: &proto.SetConfigRequest{
				DnsRouteInterval: durationpb.New(30 * time.Minute),
			},
			current: &proto.GetConfigResponse{
				DnsRouteInterval:   durationpb.New(30 * time.Minute),
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			name: "dns route interval differs from current",
			req: &proto.SetConfigRequest{
				DnsRouteInterval: durationpb.New(30 * time.Minute),
			},
			current: &proto.GetConfigResponse{
				DnsRouteInterval:   durationpb.New(60 * time.Minute),
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "extra iface blacklist entries already present in current is not a change",
			req: &proto.SetConfigRequest{
				ExtraIFaceBlacklist: []string{"docker0", "veth"},
			},
			current: &proto.GetConfigResponse{
				IfaceBlacklist:     []string{"lo", "docker0", "veth"},
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			name: "extra iface blacklist entry missing from current is a change",
			req: &proto.SetConfigRequest{
				ExtraIFaceBlacklist: []string{"docker0", "wg-custom"},
			},
			current: &proto.GetConfigResponse{
				IfaceBlacklist:     []string{"lo", "docker0", "veth"},
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "extra iface blacklist entry with empty current is a change",
			req: &proto.SetConfigRequest{
				ExtraIFaceBlacklist: []string{"docker0"},
			},
			current: &proto.GetConfigResponse{
				IfaceBlacklist:     nil,
				FullConfigSnapshot: true,
			},
			want: true,
		},
		{
			name: "extra iface blacklist same set in different order is not a change",
			req: &proto.SetConfigRequest{
				ExtraIFaceBlacklist: []string{"veth", "docker0"},
			},
			current: &proto.GetConfigResponse{
				IfaceBlacklist:     []string{"lo", "docker0", "veth"},
				FullConfigSnapshot: true,
			},
			want: false,
		},
		{
			// current came from a daemon that doesn't set FullConfigSnapshot
			// (either an old daemon predating fields 29-35, or one that
			// hasn't populated them for some other reason). It cannot be
			// trusted for a value comparison, so this falls back to the
			// presence-only check - which reports true because the request
			// carries an explicit override, even though that override's
			// value happens to match what's stored. Conservative: an extra
			// reconnect, never a silently dropped flag.
			name: "incomplete snapshot with matching override value still reports a change",
			req: &proto.SetConfigRequest{
				Mtu: &mtu,
			},
			current: &proto.GetConfigResponse{
				Mtu:                mtu,
				FullConfigSnapshot: false,
			},
			want: true,
		},
		{
			name: "incomplete snapshot with no overrides reports no change",
			req:  &proto.SetConfigRequest{},
			current: &proto.GetConfigResponse{
				Mtu:                mtu,
				FullConfigSnapshot: false,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SetConfigRequestChangesConfig(tt.req, tt.current)
			require.Equal(t, tt.want, got)
		})
	}
}
