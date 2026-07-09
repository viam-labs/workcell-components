package workcellcomponents

import (
	"testing"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

func newTestScanTunnel(t *testing.T, cfg *ScanTunnelConfig) *scanTunnel {
	t.Helper()
	base := newDecorationBase(
		resource.NewName(resource.APINamespaceRDK.WithComponentType("generic"), "scanner"),
		logging.NewTestLogger(t), ScanTunnelModel,
		nil, cfg.Label, defaultTunnelFrameColor, cfg.Color, cfg.VisualOptions)
	sides := cfg.Sides
	if sides == 0 {
		sides = 3
	}
	readers := cfg.ReaderCount
	if readers == 0 {
		readers = defaultTunnelReaderCount
	}
	return &scanTunnel{
		decorationBase: base,
		span:           defaultLen(cfg.SpanMM, defaultTunnelSpanMM),
		height:         defaultLen(cfg.HeightMM, defaultTunnelHeightMM),
		sides:          sides,
		readerCount:    readers,
		scanLineH:      cfg.ScanLineHeightMM,
	}
}

func TestScanTunnelConfig_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     ScanTunnelConfig
		wantErr bool
	}{
		{"zero config ok", ScanTunnelConfig{}, false},
		{"one-sided ok", ScanTunnelConfig{Sides: 1}, false},
		{"three-sided ok", ScanTunnelConfig{Sides: 3}, false},
		{"two-sided rejected", ScanTunnelConfig{Sides: 2}, true},
		{"reader overflow rejected", ScanTunnelConfig{ReaderCount: 13}, true},
	} {
		_, _, err := tc.cfg.Validate("")
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestScanTunnelVisuals_Composition(t *testing.T) {
	// 3-sided with a scan line: group frame + 2 posts + crossbar +
	// light bar + 3 top heads + 3 top lenses + 2 side heads + 2 side
	// lenses + scan line = 1 + 4 + 6 + 4 + 1 = 16 entries.
	tun := newTestScanTunnel(t, &ScanTunnelConfig{ScanLineHeightMM: 250})
	got := tun.buildVisuals()
	if len(got) != 16 {
		t.Fatalf("3-sided visuals: got %d entries, want 16", len(got))
	}

	// 1-sided, no scan line: drops 4 side entries + the line = 11.
	tun = newTestScanTunnel(t, &ScanTunnelConfig{Sides: 1})
	got = tun.buildVisuals()
	if len(got) != 11 {
		t.Fatalf("1-sided visuals: got %d entries, want 11", len(got))
	}

	// Scan line pulses.
	tun = newTestScanTunnel(t, &ScanTunnelConfig{ScanLineHeightMM: 250})
	var foundLine bool
	for _, v := range tun.buildVisuals() {
		if v.Label == "scanner/scan-line" {
			foundLine = true
			if v.Animation == nil {
				t.Error("scan line should carry a flicker animation")
			}
		}
	}
	if !foundLine {
		t.Error("scan line entry missing")
	}
}
