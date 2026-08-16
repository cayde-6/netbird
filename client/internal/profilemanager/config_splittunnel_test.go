package profilemanager

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigSplitTunnel(t *testing.T) {
	// wantUpdated is what drives whether apply() persists the config to disk,
	// i.e. whether the setting survives reconnects.
	tests := []struct {
		name        string
		initial     []string
		input       []string
		want        []string
		wantUpdated bool
	}{
		{
			name:        "sets the list",
			input:       []string{"gitlab.example.com"},
			want:        []string{"gitlab.example.com"},
			wantUpdated: true,
		},
		{
			name:        "nil input keeps the existing list",
			initial:     []string{"gitlab.example.com"},
			input:       nil,
			want:        []string{"gitlab.example.com"},
			wantUpdated: false,
		},
		{
			name:        "input equal to the stored list is a no-op",
			initial:     []string{"gitlab.example.com"},
			input:       []string{"gitlab.example.com"},
			want:        []string{"gitlab.example.com"},
			wantUpdated: false,
		},
		{
			name:        "empty non-nil input clears the list",
			initial:     []string{"gitlab.example.com"},
			input:       []string{},
			want:        []string{},
			wantUpdated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := &Config{SplitTunnel: tc.initial}
			input := ConfigInput{SplitTunnel: tc.input}

			updated := applySplitTunnel(config, input)

			require.Equal(t, tc.want, config.SplitTunnel)
			require.Equal(t, tc.wantUpdated, updated, "return value decides whether the config is written to disk")
		})
	}
}
