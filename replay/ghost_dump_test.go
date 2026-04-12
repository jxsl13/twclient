package replay_test

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/jxsl13/twclient/replay"
	"github.com/jxsl13/twclient/replay/ghost"
)

func TestDumpGhostTrajectory(t *testing.T) {
	const ghostFile = "../testdata/Tutorial.gho"
	if _, err := os.Stat(ghostFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", ghostFile)
	}

	g, err := ghost.Open(ghostFile)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}
	defer g.Close()

	info := g.Info()
	t.Logf("ghost: map=%s player=%s ticks=%d time=%dcs", info.Map, info.Player, info.NumTicks, info.TimeCentis)

	t.Logf("%-6s %8s %8s %6s %6s %6s %6s %4s %3s %6s %8s %8s %6s",
		"Frame", "X", "Y", "TileX", "TileY", "VelX", "VelY", "Dir", "Wpn", "HkSt", "HookX", "HookY", "AtkTk")

	var prev *replay.CharacterFrame
	for i := 0; ; i++ {
		frame, err := g.NextCharacter()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextCharacter at frame %d: %v", i, err)
		}

		if i >= 100 && i <= 200 {
			tile := replay.WorldToTile(frame.X, frame.Y)
			dxStr := ""
			dyStr := ""
			if prev != nil {
				dxStr = fmt.Sprintf(" dx=%-4d", frame.X-prev.X)
				dyStr = fmt.Sprintf(" dy=%-4d", frame.Y-prev.Y)
			}
			t.Logf("%-6d %8d %8d %6d %6d %6d %6d %4d %3d %6d %8d %8d %6d%s%s",
				i, frame.X, frame.Y, tile.X, tile.Y,
				frame.VelX, frame.VelY,
				frame.Direction, frame.Weapon, frame.HookState,
				frame.HookX, frame.HookY, frame.AttackTick,
				dxStr, dyStr)
		}
		cp := frame
		prev = &cp
		if i > 200 {
			break
		}
	}

	// Also dump the processed InputFrames from the replayer
	g2, err := ghost.Open(ghostFile)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}
	firstFrame, peekErr := g2.NextCharacter()
	adapter := replay.NewPeekedCharAdapter(g2, firstFrame, peekErr == nil)
	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	t.Logf("\n=== REPLAYER INPUT FRAMES 120-450 (after jump processing) ===")
	t.Logf("%-6s %8s %8s %6s %6s %6s %8s %8s",
		"Frame", "ExpX", "ExpY", "Dir", "Jump", "Hook", "TargetX", "TargetY")
	for i := 120; i <= 450 && i < rpl.NumFrames(); i++ {
		ex, ey, _ := rpl.ExpectedPos(i)
		inp, _ := rpl.ReplayFrame(i)
		t.Logf("%-6d %8d %8d %6d %6d %6d %8d %8d",
			i, ex, ey, inp.Direction, inp.Jump, inp.Hook, inp.TargetX, inp.TargetY)
	}
}
