package replay

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/jxsl13/twmap"
)

func TestMapScan(t *testing.T) {
	data, err := os.ReadFile("../testdata/maps/Tutorial.map")
	if err != nil {
		t.Fatal(err)
	}
	m, err := twmap.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	grid := NewTileGrid(m)

	// Focused scan: x=70-100, y=73-82
	fmt.Println("=== x=70-100, y=73-82 (focused on barrier) ===")
	fmt.Print("       ")
	for x := 70; x <= 100; x++ {
		fmt.Printf("%d", (x/10)%10)
	}
	fmt.Println()
	fmt.Print("       ")
	for x := 70; x <= 100; x++ {
		fmt.Printf("%d", x%10)
	}
	fmt.Println()
	for y := 73; y <= 82; y++ {
		fmt.Printf("y=%2d:  ", y)
		for x := 70; x <= 100; x++ {
			t := grid.At(x, y)
			switch t {
			case 0:
				fmt.Print(".")
			case 1:
				fmt.Print("#")
			case 3:
				fmt.Print("U")
			default:
				fmt.Printf("%d", t%10)
			}
		}
		fmt.Println()
	}

	// Test A* path from (96,80) to (86,78)
	start := TilePos{96, 80}
	goal := TilePos{86, 78}
	path := FindPath(grid, start, goal)
	if path == nil {
		fmt.Println("\nA* FAILED: no path from (96,80) to (86,78)")
	} else {
		fmt.Printf("\nA* path from (96,80) to (86,78): %d steps\n", len(path))
		for _, p := range path {
			fmt.Printf("  (%d,%d)\n", p.X, p.Y)
		}
	}

	// Test paths through the barrier gap at x=97, y=66-74
	for _, g2 := range []TilePos{
		{98, 72}, {98, 73}, {98, 74}, {97, 72},
		{102, 74}, {102, 80},
	} {
		p2 := FindPath(grid, TilePos{96, 80}, g2)
		if p2 == nil {
			fmt.Printf("\nA* FAILED: (96,80) to (%d,%d)\n", g2.X, g2.Y)
		} else {
			fmt.Printf("\nA* path (96,80) to (%d,%d): %d steps\n", g2.X, g2.Y, len(p2))
			for _, step := range p2 {
				fmt.Printf("  (%d,%d)\n", step.X, step.Y)
			}
		}
	}

	// Detailed tile data at x=95-100, y=70-82
	fmt.Println("\n=== Exact tiles x=94-101, y=70-82 ===")
	for y := 70; y <= 82; y++ {
		fmt.Printf("y=%2d: ", y)
		for x := 94; x <= 101; x++ {
			id := grid.At(x, y)
			fmt.Printf("%3d ", id)
		}
		fmt.Println()
	}

	// Check what's blocking at x=97
	fmt.Println("\n=== Column x=97, y=60-82 ===")
	for y := 60; y <= 82; y++ {
		id := grid.At(97, y)
		solid := grid.isSolid(97, y)
		passable := grid.isPassable(97, y)
		fmt.Printf("  (97,%d): id=%d solid=%v passable=%v\n", y, id, solid, passable)
	}

	// Scan ALL non-air tiles in x=84-100, y=66-82
	fmt.Println("\n=== Non-air tiles x=84-100, y=66-82 ===")
	for y := 66; y <= 82; y++ {
		for x := 84; x <= 100; x++ {
			id := grid.At(x, y)
			if id != 0 {
				dangerous := grid.isDangerous(x, y)
				fmt.Printf("  (%d,%d): id=%d dangerous=%v\n", x, y, id, dangerous)
			}
		}
	}

	// Terrain scan at x=115-145, y=66-82 (post-barrier section)
	fmt.Println("\n=== Post-barrier terrain x=115-145, y=66-85 ===")
	for y := 66; y <= 85; y++ {
		fmt.Printf("y=%2d: ", y)
		for x := 115; x <= 145; x++ {
			id := grid.At(x, y)
			switch {
			case id == 0:
				fmt.Print(".")
			case id == 1:
				fmt.Print("#")
			case id == 3:
				fmt.Print("U")
			default:
				fmt.Printf("%d", id)
			}
		}
		fmt.Println()
	}
}

func TestMapBarrierArea(t *testing.T) {
	data, err := os.ReadFile("../testdata/maps/Tutorial.map")
	if err != nil {
		t.Fatal(err)
	}
	m, err := twmap.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	grid := NewTileGrid(m)

	// Stuck area: tee at tile (65,79) = world (2097,2545)
	// Ghost goes to tile (74,70) = world (2396,2265)
	fmt.Println("=== Barrier area: x=58-82, y=69-82 ===")
	fmt.Print("       ")
	for x := 58; x <= 82; x++ {
		fmt.Printf("%d", (x/10)%10)
	}
	fmt.Println()
	fmt.Print("       ")
	for x := 58; x <= 82; x++ {
		fmt.Printf("%d", x%10)
	}
	fmt.Println()
	for y := 69; y <= 82; y++ {
		fmt.Printf("y=%2d:  ", y)
		for x := 58; x <= 82; x++ {
			id := grid.At(x, y)
			switch id {
			case 0:
				fmt.Print(".")
			case 1:
				fmt.Print("#")
			case 3:
				fmt.Print("U")
			default:
				fmt.Printf("%d", id%10)
			}
		}
		fmt.Println()
	}

	// Check exact tiles in column x=65
	fmt.Println("\n=== Column x=65, y=74-82 ===")
	for y := 74; y <= 82; y++ {
		id := grid.At(65, y)
		fmt.Printf("  (65,%d): id=%d solid=%v passable=%v\n", y, id, grid.isSolid(65, y), grid.isPassable(65, y))
	}

	// Check exact tiles in column x=66
	fmt.Println("\n=== Column x=66, y=74-82 ===")
	for y := 74; y <= 82; y++ {
		id := grid.At(66, y)
		fmt.Printf("  (66,%d): id=%d solid=%v passable=%v\n", y, id, grid.isSolid(66, y), grid.isPassable(66, y))
	}

	// A* from (65,79) towards various targets
	for _, goal := range []TilePos{
		{62, 75}, // back-left, above platform
		{62, 74}, // back-left, higher
		{69, 75}, // forward right on platform
		{74, 70}, // far right (where ghost goes after barrier)
	} {
		path := FindPath(grid, TilePos{65, 79}, goal)
		if path == nil {
			fmt.Printf("\nA* FAILED: (65,79) to (%d,%d)\n", goal.X, goal.Y)
		} else {
			fmt.Printf("\nA* path (65,79) to (%d,%d): %d steps\n", goal.X, goal.Y, len(path))
			for i, step := range path {
				if i < 20 || i > len(path)-5 {
					fmt.Printf("  [%d] (%d,%d)\n", i, step.X, step.Y)
				}
			}
		}
	}
}
