package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/client/proto"
	"github.com/netbirdio/netbird/client/server"
)

// TestShouldReapplyConfig covers only what belongs to shouldReapplyConfig
// itself, not to the SetConfigRequestChangesConfig logic it wraps (that
// belongs to, and is already covered by, config_diff_test.go in
// client/server): that profileSwitched short-circuits to true regardless of
// what the comparison would otherwise say, and that with profileSwitched
// false the result is exactly whatever SetConfigRequestChangesConfig
// returns.
func TestShouldReapplyConfig(t *testing.T) {
	var mtu int64 = 1280

	tests := []struct {
		name            string
		req             *proto.SetConfigRequest
		current         *proto.GetConfigResponse
		profileSwitched bool
		want            bool
	}{
		{
			name:            "profile switch overrides an otherwise no-op request",
			req:             &proto.SetConfigRequest{},
			current:         nil,
			profileSwitched: true,
			want:            true,
		},
		{
			name:            "profile switch overrides an otherwise unchanged value",
			req:             &proto.SetConfigRequest{Mtu: &mtu},
			current:         &proto.GetConfigResponse{Mtu: mtu, FullConfigSnapshot: true},
			profileSwitched: true,
			want:            true,
		},
		{
			name:            "no profile switch, no override: delegates to SetConfigRequestChangesConfig",
			req:             &proto.SetConfigRequest{},
			current:         nil,
			profileSwitched: false,
			want:            false,
		},
		{
			name:            "no profile switch, with override: delegates to SetConfigRequestChangesConfig",
			req:             &proto.SetConfigRequest{Mtu: &mtu},
			current:         nil,
			profileSwitched: false,
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReapplyConfig(tt.profileSwitched, tt.req, tt.current)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.profileSwitched || server.SetConfigRequestChangesConfig(tt.req, tt.current), got)
		})
	}
}
