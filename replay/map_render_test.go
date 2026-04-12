package replay

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"github.com/jxsl13/twmap"
	_ "github.com/jxsl13/twmap/external"
)

// TestRenderBarrierAreas renders the three barrier areas where the ghost replay
// tee gets stuck (unhookable walls requiring back-up-and-jump maneuvers).
// Output goes to replay/testdata/ for visual inspection.
func TestRenderBarrierAreas(t *testing.T) {
	data, err := os.ReadFile("../testdata/maps/Tutorial.map")
	if err != nil {
		t.Fatal(err)
	}

	type region struct {
		name string
		// tile coordinates
		minX, minY, maxX, maxY int
		// camera center (tile coords) for parallax layers
		camX, camY int
	}

	regions := []region{
		{
			name: "full_race_path",
			minX: 0, minY: 55,
			maxX: 250, maxY: 90,
			camX: 125, camY: 75,
		},
		{
			name: "barrier1_x2097",
			// World x≈2097 = tile 65, y≈2545 = tile 79
			// Ghost jumps from tile (65,79) over wall to (74,70)
			minX: 55, minY: 62,
			maxX: 82, maxY: 84,
			camX: 68, camY: 75,
		},
		{
			name: "barrier2_x3089",
			// World x≈3089 = tile 96, y≈2577 = tile 80
			// Unhookable wall at tile 97, ghost backs up and jumps
			minX: 84, minY: 62,
			maxX: 110, maxY: 84,
			camX: 97, camY: 75,
		},
		{
			name: "barrier3_x4239",
			// World x≈4239 = tile 132, y≈2353 = tile 73
			// Third barrier at around tile 132
			minX: 115, minY: 58,
			maxX: 145, maxY: 82,
			camX: 130, camY: 72,
		},
		{
			name: "start_area",
			minX: 0, minY: 68,
			maxX: 35, maxY: 84,
			camX: 17, camY: 78,
		},
	}

	for _, r := range regions {
		t.Run(r.name, func(t *testing.T) {
			img, err := twmap.Render(
				bytes.NewReader(data),
				twmap.WithRegion(twmap.MapBounds{
					MinX: r.minX, MinY: r.minY,
					MaxX: r.maxX, MaxY: r.maxY,
				}),
				twmap.WithCameraAt(r.camX, r.camY),
				twmap.WithDetail(true),
			)
			if err != nil {
				t.Fatal(err)
			}

			outPath := "testdata/" + r.name + ".png"
			f, err := os.Create(outPath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			t.Logf("rendered %s: %dx%d → %s", r.name, img.Bounds().Dx(), img.Bounds().Dy(), outPath)
		})
	}
}
