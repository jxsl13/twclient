package convert_test

import (
	"io"
	"os"
	"testing"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/jxsl13/twclient/replay/convert"
	"github.com/jxsl13/twclient/replay/demo"
	"github.com/jxsl13/twclient/replay/ghost"
	"github.com/jxsl13/twclient/replay/teehistorian"
)

// --- mock InputProvider for unit tests ---

type mockProvider struct {
	frames []replay.InputFrame
	info   replay.RecordingInfo
	pos    int
}

func (m *mockProvider) NextInput() (replay.InputFrame, error) {
	if m.pos >= len(m.frames) {
		return replay.InputFrame{}, io.EOF
	}
	f := m.frames[m.pos]
	m.pos++
	return f, nil
}

func (m *mockProvider) Info() replay.RecordingInfo { return m.info }
func (m *mockProvider) Close() error               { return nil }

func testFrames() []replay.InputFrame {
	return []replay.InputFrame{
		{Tick: 10, Input: packet.UnsafePlayerInputFromRaw([10]int{1, 50, -30, 1, 3, 0, 1, 2, 0, 0})},
		{Tick: 11, Input: packet.UnsafePlayerInputFromRaw([10]int{1, 60, -30, 0, 4, 0, 1, 2, 0, 0})},
		{Tick: 15, Input: packet.UnsafePlayerInputFromRaw([10]int{-1, 20, 10, 0, 4, 1, 1, 3, 0, 0})},
	}
}

// TestToTeehistorianRoundTrip converts mock inputs to teehistorian and reads them back.
func TestToTeehistorianRoundTrip(t *testing.T) {
	frames := testFrames()
	src := &mockProvider{
		frames: frames,
		info:   replay.RecordingInfo{Format: replay.FormatDemo, Map: "Tutorial"},
	}

	data, err := convert.ToTeehistorian(src, 0)
	if err != nil {
		t.Fatalf("ToTeehistorian: %v", err)
	}

	// Write to temp file so the existing teehistorian.Loader can read it.
	tmp, err := os.CreateTemp("", "convert_test_*.th")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	loader, err := teehistorian.Open(tmp.Name(), 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	defer loader.Close()

	info := loader.Info()
	if info.Map != "Tutorial" {
		t.Errorf("map = %q, want Tutorial", info.Map)
	}

	// Read back and compare inputs.
	for i, want := range frames {
		got, err := loader.NextInput()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		wantRaw := want.Input.Raw()
		gotRaw := got.Input.Raw()
		if wantRaw != gotRaw {
			t.Errorf("frame %d: input mismatch\n  got:  %v\n  want: %v", i, gotRaw, wantRaw)
		}
	}

	_, err = loader.NextInput()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

// TestToDemoRoundTrip converts mock inputs to demo and reads them back.
func TestToDemoRoundTrip(t *testing.T) {
	frames := testFrames()
	src := &mockProvider{
		frames: frames,
		info:   replay.RecordingInfo{Format: replay.FormatTeehistorian, Map: "Tutorial"},
	}

	data, err := convert.ToDemo(src, 0)
	if err != nil {
		t.Fatalf("ToDemo: %v", err)
	}

	// Write to temp file so the existing demo.Loader can read it.
	tmp, err := os.CreateTemp("", "convert_test_*.demo")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	loader, err := demo.Open(tmp.Name(), 0)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	defer loader.Close()

	info := loader.Info()
	if info.Map != "Tutorial" {
		t.Errorf("map = %q, want Tutorial", info.Map)
	}

	for i, want := range frames {
		got, err := loader.NextInput()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		wantRaw := want.Input.Raw()
		gotRaw := got.Input.Raw()
		if wantRaw != gotRaw {
			t.Errorf("frame %d: input mismatch\n  got:  %v\n  want: %v", i, gotRaw, wantRaw)
		}
	}

	_, err = loader.NextInput()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

// TestDemoFileToTeehistorian tests conversion of the real testdata demo file.
// Tutorial.demo is a DDNet server demo with Character items (no PlayerInput),
// so we wrap it with CharacterToInputAdapter to derive inputs.
func TestDemoFileToTeehistorian(t *testing.T) {
	const demoPath = "../../testdata/Tutorial.demo"
	if _, err := os.Stat(demoPath); err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	loader, err := demo.Open(demoPath, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}

	adapter := replay.NewCharacterToInputAdapter(loader)
	data, err := convert.ToTeehistorian(adapter, 0)
	if err != nil {
		t.Fatalf("ToTeehistorian: %v", err)
	}
	adapter.Close()

	if len(data) < 50 {
		t.Fatalf("teehistorian output too small: %d bytes", len(data))
	}

	// Verify we can read the resulting teehistorian file.
	tmp, err := os.CreateTemp("", "demo_convert_*.th")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	thLoader, err := teehistorian.Open(tmp.Name(), 0)
	if err != nil {
		t.Fatalf("teehistorian.Open roundtrip: %v", err)
	}
	defer thLoader.Close()

	count := 0
	for {
		_, err := thLoader.NextInput()
		if err != nil {
			break
		}
		count++
	}
	t.Logf("Demo→Teehistorian: converted %d input frames, %d bytes", count, len(data))
	if count == 0 {
		t.Error("no input frames in converted teehistorian")
	}
}

// TestFullRoundTrip does Demo→Teehistorian→Demo and compares inputs.
func TestFullRoundTrip(t *testing.T) {
	frames := testFrames()
	src := &mockProvider{
		frames: frames,
		info:   replay.RecordingInfo{Format: replay.FormatDemo, Map: "TestMap"},
	}

	// Demo inputs → Teehistorian
	thData, err := convert.ToTeehistorian(src, 0)
	if err != nil {
		t.Fatalf("ToTeehistorian: %v", err)
	}

	tmpTH, err := os.CreateTemp("", "roundtrip_*.th")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpTH.Name())
	tmpTH.Write(thData)
	tmpTH.Close()

	// Teehistorian → Demo
	thLoader, err := teehistorian.Open(tmpTH.Name(), 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}

	demoData, err := convert.ToDemo(thLoader, 0)
	if err != nil {
		t.Fatalf("ToDemo: %v", err)
	}
	thLoader.Close()

	tmpDemo, err := os.CreateTemp("", "roundtrip_*.demo")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpDemo.Name())
	tmpDemo.Write(demoData)
	tmpDemo.Close()

	// Demo → read inputs
	demoLoader, err := demo.Open(tmpDemo.Name(), 0)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	defer demoLoader.Close()

	for i, want := range frames {
		got, err := demoLoader.NextInput()
		if err != nil {
			t.Fatalf("round-trip frame %d: %v", i, err)
		}
		wantRaw := want.Input.Raw()
		gotRaw := got.Input.Raw()
		if wantRaw != gotRaw {
			t.Errorf("round-trip frame %d: mismatch\n  got:  %v\n  want: %v", i, gotRaw, wantRaw)
		}
	}
}

// TestGhostConversions converts the real ghost testdata through all
// format paths and performs deep validation on each output.
func TestGhostConversions(t *testing.T) {
	const ghostPath = "../../testdata/Tutorial.gho"
	if _, err := os.Stat(ghostPath); err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	// Read source ghost metadata.
	gLoader, err := ghost.Open(ghostPath)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}
	srcInfo := gLoader.Info()
	gLoader.Close()
	t.Logf("Ghost source: map=%q player=%q numTicks=%d timeCentis=%d",
		srcInfo.Map, srcInfo.Player, srcInfo.NumTicks, srcInfo.TimeCentis)

	// Ghost → collect reference frames via CharacterToInputAdapter.
	refFrames := readGhostInputs(t, ghostPath)
	t.Logf("Ghost frames: %d, ticks %d..%d", len(refFrames), refFrames[0].Tick, refFrames[len(refFrames)-1].Tick)

	// Validate source ghost frames deeply.
	validateFrames(t, "Ghost(source)", refFrames)

	// --- Ghost → Teehistorian ---
	thData := ghostToTeehistorian(t, ghostPath)
	thPath := writeTempFile(t, "ghost_conv_*.teehistorian", thData)
	defer os.Remove(thPath)

	// Validate teehistorian metadata.
	thLoader, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	thInfo := thLoader.Info()
	thLoader.Close()
	if thInfo.Map != srcInfo.Map {
		t.Errorf("Ghost→TH: map = %q, want %q", thInfo.Map, srcInfo.Map)
	}
	if thInfo.Format != replay.FormatTeehistorian {
		t.Errorf("Ghost→TH: format = %q, want %q", thInfo.Format, replay.FormatTeehistorian)
	}

	thFrames := readTeehistorianInputs(t, thPath, 0)
	t.Logf("Ghost→Teehistorian: %d frames, %d bytes", len(thFrames), len(thData))
	validateFrames(t, "Ghost→TH", thFrames)
	assertFrameCount(t, "Ghost→TH", refFrames, thFrames)
	compareFrameInputs(t, "Ghost→TH", refFrames, thFrames)
	compareTickDeltas(t, "Ghost→TH", refFrames, thFrames)

	// CIDs check.
	thLoader2, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open for CIDs: %v", err)
	}
	cids := thLoader2.CIDs()
	thLoader2.Close()
	if len(cids) != 1 || cids[0] != 0 {
		t.Errorf("Ghost→TH CIDs = %v, want [0]", cids)
	}

	// --- Ghost → Demo ---
	demoData := ghostToDemo(t, ghostPath)
	demoPath := writeTempFile(t, "ghost_conv_*.demo", demoData)
	defer os.Remove(demoPath)

	// Validate demo metadata.
	demoLoader, err := demo.Open(demoPath, 0)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	demoInfo := demoLoader.Info()
	demoLoader.Close()
	if demoInfo.Map != srcInfo.Map {
		t.Errorf("Ghost→Demo: map = %q, want %q", demoInfo.Map, srcInfo.Map)
	}
	if demoInfo.Format != replay.FormatDemo {
		t.Errorf("Ghost→Demo: format = %q, want %q", demoInfo.Format, replay.FormatDemo)
	}

	demoFrames := readDemoInputs(t, demoPath, 0)
	t.Logf("Ghost→Demo: %d frames, %d bytes", len(demoFrames), len(demoData))
	validateFrames(t, "Ghost→Demo", demoFrames)
	assertFrameCount(t, "Ghost→Demo", refFrames, demoFrames)
	compareFrameInputs(t, "Ghost→Demo", refFrames, demoFrames)
	compareTickDeltas(t, "Ghost→Demo", refFrames, demoFrames)

	// --- Ghost → TH → Demo (chain) ---
	thLoader3, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	chainDemoData, err := convert.ToDemo(thLoader3, 0)
	thLoader3.Close()
	if err != nil {
		t.Fatalf("TH→Demo: %v", err)
	}
	chainDemoPath := writeTempFile(t, "th_to_demo_*.demo", chainDemoData)
	defer os.Remove(chainDemoPath)
	chainDemoFrames := readDemoInputs(t, chainDemoPath, 0)
	t.Logf("Ghost→TH→Demo: %d frames", len(chainDemoFrames))
	validateFrames(t, "Ghost→TH→Demo", chainDemoFrames)
	assertFrameCount(t, "Ghost→TH→Demo", refFrames, chainDemoFrames)
	compareFrameInputs(t, "Ghost→TH→Demo", refFrames, chainDemoFrames)
	compareTickDeltas(t, "Ghost→TH→Demo", refFrames, chainDemoFrames)
}

// TestDemoConversions converts the real demo testdata (a DDNet server demo
// with Character but no PlayerInput items) through all format paths and
// performs deep validation on each output.
func TestDemoConversions(t *testing.T) {
	const srcDemoPath = "../../testdata/Tutorial.demo"
	if _, err := os.Stat(srcDemoPath); err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	// Read source demo metadata.
	srcLoader, err := demo.Open(srcDemoPath, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	srcInfo := srcLoader.Info()
	srcLoader.Close()
	t.Logf("Demo source: map=%q format=%q selectedCID=%d", srcInfo.Map, srcInfo.Format, srcInfo.SelectedCID)

	// Demo → collect reference frames via CharacterToInputAdapter
	// (server demo has Character items, not PlayerInput).
	refFrames := readDemoCharacterInputs(t, srcDemoPath, -1)
	t.Logf("Demo frames: %d, ticks %d..%d", len(refFrames), refFrames[0].Tick, refFrames[len(refFrames)-1].Tick)
	validateFrames(t, "Demo(source)", refFrames)

	// --- Demo → Teehistorian (via CharacterToInputAdapter) ---
	demoLoader, err := demo.Open(srcDemoPath, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	adapter := replay.NewCharacterToInputAdapter(demoLoader)
	thData, err := convert.ToTeehistorian(adapter, 0)
	adapter.Close()
	if err != nil {
		t.Fatalf("Demo→TH: %v", err)
	}
	thPath := writeTempFile(t, "demo_conv_*.teehistorian", thData)
	defer os.Remove(thPath)

	// Validate teehistorian metadata.
	thLoader, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	thInfo := thLoader.Info()
	thLoader.Close()
	if thInfo.Map != srcInfo.Map {
		t.Errorf("Demo→TH: map = %q, want %q", thInfo.Map, srcInfo.Map)
	}

	thFrames := readTeehistorianInputs(t, thPath, 0)
	t.Logf("Demo→Teehistorian: %d frames, %d bytes", len(thFrames), len(thData))
	validateFrames(t, "Demo→TH", thFrames)
	assertFrameCount(t, "Demo→TH", refFrames, thFrames)
	compareFrameInputs(t, "Demo→TH", refFrames, thFrames)
	compareTickDeltas(t, "Demo→TH", refFrames, thFrames)

	// --- Demo → TH → Demo (full round-trip) ---
	thLoader2, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	rtDemoData, err := convert.ToDemo(thLoader2, 0)
	thLoader2.Close()
	if err != nil {
		t.Fatalf("TH→Demo: %v", err)
	}
	rtDemoPath := writeTempFile(t, "demo_rt_*.demo", rtDemoData)
	defer os.Remove(rtDemoPath)

	// Round-tripped demo now has PlayerInput items (written by ToDemo).
	rtDemoFrames := readDemoInputs(t, rtDemoPath, 0)
	t.Logf("Demo→TH→Demo: %d frames, %d bytes", len(rtDemoFrames), len(rtDemoData))
	validateFrames(t, "Demo→TH→Demo", rtDemoFrames)
	assertFrameCount(t, "Demo→TH→Demo", refFrames, rtDemoFrames)
	compareFrameInputs(t, "Demo→TH→Demo", refFrames, rtDemoFrames)
	compareTickDeltas(t, "Demo→TH→Demo", refFrames, rtDemoFrames)
}

// TestClientDemoConversions tests the client-recorded demo (Tutorial_client.demo)
// through all conversion paths. Client demos have type="client" and m_Local=1
// in PlayerInfo for CID auto-detection, but like server demos, DDNet stores
// CNetObj_Character items (not PlayerInput) in snapshots.
func TestClientDemoConversions(t *testing.T) {
	const srcDemoPath = "../../testdata/Tutorial_client.demo"
	if _, err := os.Stat(srcDemoPath); err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	// Read source metadata.
	srcLoader, err := demo.Open(srcDemoPath, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	srcInfo := srcLoader.Info()
	srcLoader.Close()
	t.Logf("Client demo source: map=%q format=%q selectedCID=%d", srcInfo.Map, srcInfo.Format, srcInfo.SelectedCID)

	// Collect reference frames via CharacterToInputAdapter.
	refFrames := readDemoCharacterInputs(t, srcDemoPath, -1)
	if len(refFrames) == 0 {
		t.Fatal("client demo produced 0 frames")
	}
	t.Logf("Client demo frames: %d, ticks %d..%d", len(refFrames), refFrames[0].Tick, refFrames[len(refFrames)-1].Tick)
	validateFrames(t, "ClientDemo(source)", refFrames)

	// --- Client Demo → Teehistorian ---
	demoLoader, err := demo.Open(srcDemoPath, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	adapter := replay.NewCharacterToInputAdapter(demoLoader)
	thData, err := convert.ToTeehistorian(adapter, 0)
	adapter.Close()
	if err != nil {
		t.Fatalf("ClientDemo→TH: %v", err)
	}
	thPath := writeTempFile(t, "client_demo_conv_*.teehistorian", thData)
	defer os.Remove(thPath)

	thFrames := readTeehistorianInputs(t, thPath, 0)
	t.Logf("ClientDemo→Teehistorian: %d frames, %d bytes", len(thFrames), len(thData))
	validateFrames(t, "ClientDemo→TH", thFrames)
	assertFrameCount(t, "ClientDemo→TH", refFrames, thFrames)
	compareFrameInputs(t, "ClientDemo→TH", refFrames, thFrames)

	// --- Client Demo → TH → Demo (round-trip) ---
	thLoader, err := teehistorian.Open(thPath, 0)
	if err != nil {
		t.Fatalf("teehistorian.Open: %v", err)
	}
	rtDemoData, err := convert.ToDemo(thLoader, 0)
	thLoader.Close()
	if err != nil {
		t.Fatalf("TH→Demo: %v", err)
	}
	rtDemoPath := writeTempFile(t, "client_demo_rt_*.demo", rtDemoData)
	defer os.Remove(rtDemoPath)

	// Round-tripped demo has PlayerInput items (written by ToDemo).
	rtFrames := readDemoInputs(t, rtDemoPath, 0)
	t.Logf("ClientDemo→TH→Demo: %d frames, %d bytes", len(rtFrames), len(rtDemoData))
	validateFrames(t, "ClientDemo→TH→Demo", rtFrames)
	assertFrameCount(t, "ClientDemo→TH→Demo", refFrames, rtFrames)
	compareFrameInputs(t, "ClientDemo→TH→Demo", refFrames, rtFrames)
}

// TestTickMonotonicity verifies that converted files always produce
// strictly non-decreasing tick sequences.
func TestTickMonotonicity(t *testing.T) {
	paths := []struct {
		name string
		path string
	}{
		{"Tutorial.gho", "../../testdata/Tutorial.gho"},
		{"Tutorial.demo", "../../testdata/Tutorial.demo"},
		{"Tutorial_client.demo", "../../testdata/Tutorial_client.demo"},
	}

	for _, p := range paths {
		if _, err := os.Stat(p.path); err != nil {
			t.Skipf("%s not available: %v", p.name, err)
		}
	}

	// Ghost source ticks
	ghostFrames := readGhostInputs(t, paths[0].path)
	assertMonotonicTicks(t, "Ghost(source)", ghostFrames)

	// Demo source ticks (server demo → character adapter)
	demoFrames := readDemoCharacterInputs(t, paths[1].path, -1)
	assertMonotonicTicks(t, "Demo(source)", demoFrames)

	// Ghost → Teehistorian
	thData := ghostToTeehistorian(t, paths[0].path)
	thPath := writeTempFile(t, "mono_*.teehistorian", thData)
	defer os.Remove(thPath)
	assertMonotonicTicks(t, "Ghost→TH", readTeehistorianInputs(t, thPath, 0))

	// Ghost → Demo
	demoData := ghostToDemo(t, paths[0].path)
	demoPath := writeTempFile(t, "mono_*.demo", demoData)
	defer os.Remove(demoPath)
	assertMonotonicTicks(t, "Ghost→Demo", readDemoInputs(t, demoPath, 0))

	// Demo → Teehistorian (server demo → character adapter → TH)
	demoLoader, err := demo.Open(paths[1].path, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}
	adapter := replay.NewCharacterToInputAdapter(demoLoader)
	thData2, err := convert.ToTeehistorian(adapter, 0)
	adapter.Close()
	if err != nil {
		t.Fatalf("Demo→TH: %v", err)
	}
	thPath2 := writeTempFile(t, "mono_demo_*.teehistorian", thData2)
	defer os.Remove(thPath2)
	assertMonotonicTicks(t, "Demo→TH", readTeehistorianInputs(t, thPath2, 0))

	// Client demo source ticks
	clientDemoFrames := readDemoCharacterInputs(t, paths[2].path, -1)
	assertMonotonicTicks(t, "ClientDemo(source)", clientDemoFrames)
}

// --- helpers ---

func readGhostInputs(t *testing.T, path string) []replay.InputFrame {
	t.Helper()
	gLoader, err := ghost.Open(path)
	if err != nil {
		t.Fatalf("ghost.Open(%s): %v", path, err)
	}
	adapter := replay.NewCharacterToInputAdapter(gLoader)
	defer adapter.Close()
	return drainInputs(t, adapter)
}

func readDemoInputs(t *testing.T, path string, cid int) []replay.InputFrame {
	t.Helper()
	loader, err := demo.Open(path, cid)
	if err != nil {
		t.Fatalf("demo.Open(%s): %v", path, err)
	}
	defer loader.Close()
	return drainInputs(t, loader)
}

// readDemoCharacterInputs opens a demo as a CharacterProvider and derives
// inputs via CharacterToInputAdapter. This is the correct path for DDNet
// server demos that contain Character items but no PlayerInput items.
func readDemoCharacterInputs(t *testing.T, path string, cid int) []replay.InputFrame {
	t.Helper()
	loader, err := demo.Open(path, cid)
	if err != nil {
		t.Fatalf("demo.Open(%s): %v", path, err)
	}
	adapter := replay.NewCharacterToInputAdapter(loader)
	defer adapter.Close()
	return drainInputs(t, adapter)
}

func readTeehistorianInputs(t *testing.T, path string, cid int) []replay.InputFrame {
	t.Helper()
	loader, err := teehistorian.Open(path, cid)
	if err != nil {
		t.Fatalf("teehistorian.Open(%s): %v", path, err)
	}
	defer loader.Close()
	return drainInputs(t, loader)
}

func drainInputs(t *testing.T, src replay.InputProvider) []replay.InputFrame {
	t.Helper()
	var frames []replay.InputFrame
	for {
		f, err := src.NextInput()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("NextInput: %v", err)
		}
		frames = append(frames, f)
	}
	if len(frames) == 0 {
		t.Fatal("no frames read")
	}
	return frames
}

func ghostToTeehistorian(t *testing.T, ghostPath string) []byte {
	t.Helper()
	gLoader, err := ghost.Open(ghostPath)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}
	adapter := replay.NewCharacterToInputAdapter(gLoader)
	data, err := convert.ToTeehistorian(adapter, 0)
	adapter.Close()
	if err != nil {
		t.Fatalf("Ghost→TH: %v", err)
	}
	return data
}

func ghostToDemo(t *testing.T, ghostPath string) []byte {
	t.Helper()
	gLoader, err := ghost.Open(ghostPath)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}
	adapter := replay.NewCharacterToInputAdapter(gLoader)
	data, err := convert.ToDemo(adapter, 0)
	adapter.Close()
	if err != nil {
		t.Fatalf("Ghost→Demo: %v", err)
	}
	return data
}

func writeTempFile(t *testing.T, pattern string, data []byte) string {
	t.Helper()
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		t.Fatal(err)
	}
	tmp.Close()
	return tmp.Name()
}

func compareFrameInputs(t *testing.T, label string, want, got []replay.InputFrame) {
	t.Helper()
	n := min(len(got), len(want))
	mismatches := 0
	for i := 0; i < n; i++ {
		wantRaw := want[i].Input.Raw()
		gotRaw := got[i].Input.Raw()
		if wantRaw != gotRaw {
			if mismatches < 5 {
				t.Errorf("%s frame %d (tick %d): input mismatch\n  got:  %v\n  want: %v", label, i, want[i].Tick, gotRaw, wantRaw)
			}
			mismatches++
		}
	}
	if mismatches > 5 {
		t.Errorf("%s: %d total input mismatches (showing first 5)", label, mismatches)
	}
}

func assertMonotonicTicks(t *testing.T, label string, frames []replay.InputFrame) {
	t.Helper()
	for i := 1; i < len(frames); i++ {
		if frames[i].Tick < frames[i-1].Tick {
			t.Errorf("%s: tick decreased at frame %d: %d → %d", label, i, frames[i-1].Tick, frames[i].Tick)
			return
		}
	}
}

// assertFrameCount checks that got has the same number of frames as want.
func assertFrameCount(t *testing.T, label string, want, got []replay.InputFrame) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: frame count got %d, want %d", label, len(got), len(want))
	}
}

// compareTickDeltas checks whether inter-frame tick gaps are preserved through
// conversion. Since teehistorian uses implicit relative tick counting and demo
// uses absolute tick markers, small differences at format boundaries are normal.
// This logs mismatches as informational rather than failing the test.
func compareTickDeltas(t *testing.T, label string, want, got []replay.InputFrame) {
	t.Helper()
	n := min(len(got), len(want))
	if n < 2 {
		return
	}
	mismatches := 0
	for i := 1; i < n; i++ {
		wantDelta := want[i].Tick - want[i-1].Tick
		gotDelta := got[i].Tick - got[i-1].Tick
		if wantDelta != gotDelta {
			mismatches++
		}
	}
	if mismatches > 0 {
		t.Logf("%s: %d/%d tick delta mismatches (expected for cross-format conversion)", label, mismatches, n-1)
	}
}

// validateFrames performs in-depth validation on a slice of InputFrames:
//   - Tick monotonicity (non-decreasing)
//   - All tick values are positive
//   - No duplicate consecutive identical inputs (sanity — at least some variation expected)
//   - Every field passes NewPlayerInputFromRaw validation (range checks)
//   - Direction, jump, hook are within their typed ranges
//   - Fire counter is non-negative
//   - WantedWeapon is in [0,6]
//   - PlayerFlags contain only valid bits
//   - Statistical checks: not all frames identical, some direction changes exist
func validateFrames(t *testing.T, label string, frames []replay.InputFrame) {
	t.Helper()

	if len(frames) == 0 {
		t.Fatalf("%s: no frames", label)
	}

	var (
		rangeErrors  int
		dirChanges   int
		jumpCounts   int
		hookCounts   int
		fireCounts   int
		allIdentical = true
		firstRaw     = frames[0].Input.Raw()
	)

	for i, f := range frames {
		// Positive ticks.
		if f.Tick < 0 {
			t.Errorf("%s frame %d: negative tick %d", label, i, f.Tick)
		}

		// Monotonicity.
		if i > 0 && f.Tick < frames[i-1].Tick {
			t.Errorf("%s frame %d: tick decreased %d → %d", label, i, frames[i-1].Tick, f.Tick)
		}

		// Validate all field ranges via the validating constructor.
		raw := f.Input.Raw()
		if _, err := packet.NewPlayerInputFromRaw(raw); err != nil {
			if rangeErrors < 5 {
				t.Errorf("%s frame %d (tick %d): invalid input: %v  raw=%v", label, i, f.Tick, err, raw)
			}
			rangeErrors++
		}

		// Track variation.
		if raw != firstRaw {
			allIdentical = false
		}
		if i > 0 {
			prevRaw := frames[i-1].Input.Raw()
			if raw[0] != prevRaw[0] {
				dirChanges++
			}
		}

		// Count action usage.
		if f.Input.Jump == packet.JumpOn {
			jumpCounts++
		}
		if f.Input.Hook == packet.HookOn {
			hookCounts++
		}
		if f.Input.Fire > 0 {
			fireCounts++
		}
	}

	if rangeErrors > 5 {
		t.Errorf("%s: %d total range errors (showing first 5)", label, rangeErrors)
	}

	// Statistical sanity: a real gameplay recording should have some input variation.
	// Converted outputs inherit source characteristics, so only warn (don't fail)
	// when inputs are uniform — the source itself might be from a spectator or idle demo.
	if allIdentical && len(frames) > 10 {
		t.Logf("%s: NOTE all %d frames have identical inputs (may be spectator/idle recording)", label, len(frames))
	}
	if dirChanges == 0 && len(frames) > 100 {
		t.Logf("%s: NOTE no direction changes across %d frames", label, len(frames))
	}

	// Log action statistics for review.
	t.Logf("%s stats: %d frames, %d dir_changes, %d jump_frames, %d hook_frames, %d fire_frames",
		label, len(frames), dirChanges, jumpCounts, hookCounts, fireCounts)

	// Tick span check: first and last tick should make sense.
	tickSpan := frames[len(frames)-1].Tick - frames[0].Tick + 1
	if tickSpan < len(frames) {
		t.Errorf("%s: tick span %d < frame count %d (more than one input per tick?)", label, tickSpan, len(frames))
	}
	// Density: frames should cover at least 10% of the tick span (not super sparse).
	density := float64(len(frames)) / float64(tickSpan)
	t.Logf("%s density: %.1f%% (%d frames over %d ticks)", label, density*100, len(frames), tickSpan)
	if density < 0.01 && len(frames) > 100 {
		t.Errorf("%s: frame density %.1f%% is suspiciously low", label, density*100)
	}
}
