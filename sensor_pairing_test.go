package workcellcomponents

import (
	"context"
	"errors"
	"testing"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/services/worldstatestore"
)

// fakeStore answers DoCommand from a per-verb table and records the verbs
// it was asked. Unknown verbs get an empty map and no error, the way some
// stores answer them.
type fakeStore struct {
	worldstatestore.Service
	replies map[string]map[string]interface{}
	errs    map[string]error
	asked   []string
}

func (f *fakeStore) DoCommand(
	_ context.Context, cmd map[string]interface{},
) (map[string]interface{}, error) {
	for verb := range cmd {
		f.asked = append(f.asked, verb)
		if err := f.errs[verb]; err != nil {
			return nil, err
		}
		if r, ok := f.replies[verb]; ok {
			return r, nil
		}
		return map[string]interface{}{}, nil
	}
	return nil, errors.New("empty command")
}

func newTestPalletEmpty(t *testing.T, store *fakeStore) *palletEmpty {
	t.Helper()
	return &palletEmpty{
		logger:             logging.NewTestLogger(t),
		store:              store,
		storeNameForErrors: "pack-sequencer",
	}
}

// pack-sequencer 0.3.0 answers get_progress with placed_count, and numbers
// arrive as float64 after the protobuf Struct round-trip.
func TestPalletEmptyReadsPackSequencer030(t *testing.T) {
	store := &fakeStore{replies: map[string]map[string]interface{}{
		"get_progress": {"placed_count": 3.0, "total": 8.0, "complete": false,
			"next_seq": 4.0, "failed_count": 0.0},
	}}
	p := newTestPalletEmpty(t, store)
	ctx := context.Background()

	rd, err := p.Readings(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rd["pallet_empty"] != false || rd["pallet_full"] != false ||
		rd["boxes_on_pallet"] != 3 || rd["capacity"] != 8 {
		t.Fatalf("3 of 8 placed: got %v", rd)
	}

	store.replies["get_progress"] = map[string]interface{}{
		"placed_count": 0.0, "total": 8.0, "complete": false}
	if rd, _ = p.Readings(ctx, nil); rd["pallet_empty"] != true {
		t.Fatalf("0 placed must read empty: %v", rd)
	}

	store.replies["get_progress"] = map[string]interface{}{
		"placed_count": 8.0, "total": 8.0, "complete": false}
	if rd, _ = p.Readings(ctx, nil); rd["pallet_full"] != true {
		t.Fatalf("8 of 8 placed must read full: %v", rd)
	}

	store.replies["get_progress"] = map[string]interface{}{
		"placed_count": 5.0, "total": 0.0, "complete": true}
	if rd, _ = p.Readings(ctx, nil); rd["pallet_full"] != true {
		t.Fatalf("complete must read full even without a total: %v", rd)
	}
}

// A store that answers neither verb with a placement count must surface an
// error, never a reading of pallet_empty: true.
func TestPalletEmptyNoProgressShapeIsAnError(t *testing.T) {
	ctx := context.Background()

	empty := newTestPalletEmpty(t, &fakeStore{})
	if rd, err := empty.Readings(ctx, nil); !errors.Is(err, errNoProgressShape) || rd != nil {
		t.Fatalf("empty answers: rd=%v err=%v, want errNoProgressShape", rd, err)
	}

	boom := errors.New("store down")
	failing := newTestPalletEmpty(t, &fakeStore{errs: map[string]error{
		"get_progress": boom, "get_status": boom}})
	if rd, err := failing.Readings(ctx, nil); !errors.Is(err, boom) || rd != nil {
		t.Fatalf("erroring store: rd=%v err=%v, want the store's error", rd, err)
	}
}

// 0.4.0 and later answer get_status with placed. Once a verb works, later
// readings ask only that verb.
func TestPalletEmptyFallsBackToGetStatusAndCachesIt(t *testing.T) {
	store := &fakeStore{
		errs: map[string]error{"get_progress": errors.New("unknown verb")},
		replies: map[string]map[string]interface{}{
			"get_status": {"placed": 2.0, "total": 8.0, "complete": false},
		},
	}
	p := newTestPalletEmpty(t, store)
	ctx := context.Background()

	rd, err := p.Readings(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rd["boxes_on_pallet"] != 2 || rd["pallet_empty"] != false {
		t.Fatalf("get_status fallback: got %v", rd)
	}
	if _, err := p.Readings(ctx, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"get_progress", "get_status", "get_status"}
	if len(store.asked) != len(want) {
		t.Fatalf("verbs asked = %v, want %v", store.asked, want)
	}
	for i := range want {
		if store.asked[i] != want[i] {
			t.Fatalf("verbs asked = %v, want %v", store.asked, want)
		}
	}
}

func TestBoxDetectResetWhileDisabled(t *testing.T) {
	b := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60})
	ctx := context.Background()
	out, err := b.DoCommand(ctx, map[string]interface{}{"reset": true})
	if err != nil {
		t.Fatal(err)
	}
	if out["box_present"] != false || out["error"] == nil {
		t.Fatalf("reset on a disabled infeed must not report a box: %v", out)
	}
	if rd, _ := b.Readings(ctx, nil); rd["box_present"] != false {
		t.Fatalf("readings after reset disagree with reset's answer: %v", rd)
	}
}

// fakeSensor returns fixed readings, standing in for a sensor that is not
// one of this module's.
type fakeSensor struct {
	sensor.Sensor
	readings map[string]interface{}
	err      error
}

func (f *fakeSensor) Readings(
	context.Context, map[string]interface{},
) (map[string]interface{}, error) {
	return f.readings, f.err
}

func TestInfeedStateMapsBoxDetectReadings(t *testing.T) {
	ctx := context.Background()
	station := func(s sensor.Sensor) *pickStation {
		return &pickStation{logger: logging.NewTestLogger(t), infeed: s}
	}

	if st := station(nil).infeedState(ctx); st != nil {
		t.Fatalf("unpaired station: %+v, want nil", st)
	}

	on := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60, Enabled: true})
	st := station(on).infeedState(ctx)
	wantDims := Vec3D{X: defaultInfeedBoxWidthMM, Y: defaultInfeedBoxLengthMM,
		Z: defaultInfeedBoxHeightMM}
	if st == nil || !st.present || st.fraction != 0 ||
		st.dims != wantDims || st.color != pickStationInfeedBoxColor {
		t.Fatalf("waiting box: %+v", st)
	}

	on.DoCommand(ctx, map[string]interface{}{"take": true})
	if st = station(on).infeedState(ctx); st == nil || st.present ||
		st.fraction < 0 || st.fraction > 0.05 {
		t.Fatalf("just taken: %+v, want absent near the entry edge", st)
	}

	off := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60})
	if st = station(off).infeedState(ctx); st != nil {
		t.Fatalf("disabled infeed: %+v, want nil", st)
	}

	travelling := &fakeSensor{readings: map[string]interface{}{
		"enabled": true, "box_present": false,
		"seconds_until_next": 15.0, "interval_seconds": 60.0}}
	if st = station(travelling).infeedState(ctx); st == nil || st.fraction != 0.75 {
		t.Fatalf("15 s of 60 left: %+v, want fraction 0.75", st)
	}

	noFlag := &fakeSensor{readings: map[string]interface{}{"box_present": true}}
	if st = station(noFlag).infeedState(ctx); st == nil || !st.present {
		t.Fatalf("sensor without an enabled key must stay on: %+v", st)
	}

	for name, s := range map[string]*fakeSensor{
		"not a box-detect": {readings: map[string]interface{}{"tray_present": true}},
		"read error":       {err: errors.New("boom")},
	} {
		if st = station(s).infeedState(ctx); st != nil {
			t.Fatalf("%s: %+v, want nil", name, st)
		}
	}

	custom := station(on)
	custom.cfg.InfeedBoxDimsMM = &Vec3D{X: 300, Y: 200, Z: 150}
	custom.cfg.InfeedBoxColor = &Color{R: 70, G: 150, B: 175, A: 1}
	if st = custom.infeedState(ctx); st == nil ||
		st.dims != *custom.cfg.InfeedBoxDimsMM || st.color != *custom.cfg.InfeedBoxColor {
		t.Fatalf("configured dims/color not used: %+v", st)
	}
}

func TestExchangeStateMapsTrayDockReadings(t *testing.T) {
	ctx := context.Background()
	palletWith := func(s sensor.Sensor) *pallet {
		return &pallet{logger: logging.NewTestLogger(t), dock: s}
	}

	if st := palletWith(nil).exchangeState(ctx); st != nil {
		t.Fatalf("unpaired pallet: %+v, want nil", st)
	}

	st := palletWith(&trayDock{}).exchangeState(ctx)
	if st == nil || !st.present || st.fraction != 0 ||
		st.travelMM != defaultExchangeTravelMM || st.loadHeightMM != defaultExchangeLoadHeightMM {
		t.Fatalf("docked tray: %+v", st)
	}

	exchanging := &fakeSensor{readings: map[string]interface{}{
		"tray_present": false, "seconds_until_docked": 2.0, "exchange_seconds": 8.0}}
	if st = palletWith(exchanging).exchangeState(ctx); st == nil || st.present || st.fraction != 0.75 {
		t.Fatalf("2 s of 8 left: %+v, want fraction 0.75", st)
	}

	tuned := palletWith(exchanging)
	tuned.cfg.ExchangeTravelMM = 500
	zero := 0.0
	tuned.cfg.ExchangeLoadHeightMM = &zero
	if st = tuned.exchangeState(ctx); st == nil || st.travelMM != 500 || st.loadHeightMM != 0 {
		t.Fatalf("configured travel/load not used: %+v", st)
	}

	for name, s := range map[string]*fakeSensor{
		"not a tray-dock": {readings: map[string]interface{}{"box_present": true}},
		"read error":      {err: errors.New("boom")},
	} {
		if st = palletWith(s).exchangeState(ctx); st != nil {
			t.Fatalf("%s: %+v, want nil", name, st)
		}
	}
}
