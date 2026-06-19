// driver.cpp — golden per-tick physics-vector extractor for twclient T197.
//
// Compiles against DDNet's REAL CCharacterCore / CCollision / CWorldCore
// (ddnet@c7d760d5a, src/game/gamecore.cpp + collision.cpp) and emits, per
// scenario, the QUANTIZED snapshot output of every tick:
//
//     px = round_to_int(m_Pos.x)        py = round_to_int(m_Pos.y)
//     vx = round_to_int(m_Vel.x*256)    vy = round_to_int(m_Vel.y*256)
//
// i.e. exactly what CCharacterCore::Write packs into CNetObj_CharacterCore
// (gamecore.cpp:603-625; round_to_int = math.h:17). These are GENERATED DATA,
// not DDNet source, so the JSON committed under physics/testdata/ carries no
// GPL code (V152).
//
// The JSON is SELF-DESCRIBING: each scenario records the grid, every core's
// initial pos/vel, and the FULL per-tick input vector, so the Go replay
// (physics/ddnet_vectors_test.go) drives twclient's physics.Core over the
// identical inputs WITHOUT duplicating any scenario logic, then compares its
// quantized output against the golden rows here.
//
// The map is a fabricated tile grid (all-air + one floor row + one wall
// column) injected through CCollision::InitTestGrid, a public test-only setter
// added by the sed patch in this directory (build-only, never shipped).
//
// Per tick: Tick(UseInput=true) on every core (deferred tee<->tee handled
// inside Tick via TickDeferred), then Move() on every core, then Quantize()
// (Write->Read) to mirror the server's per-tick snapshot round-trip.

#include <game/collision.h>
#include <game/gamecore.h>
#include <game/mapitems.h> // TILE_SOLID
#include <game/teamscore.h>

#include <cmath>
#include <cstdio>
#include <string>
#include <vector>

// ---- fabricated map grid ---------------------------------------------------
static const int GRID_W = 50; // tiles  (1600 px)
static const int GRID_H = 50; // tiles  (1600 px)
static const int FLOOR_TY = 40; // solid floor row     -> world y 1280..1311
static const int WALL_TX = 30; // solid wall column   -> world x  960.. 991

static CCollision g_Collision;

static void buildGrid()
{
	std::vector<unsigned char> tiles((size_t)GRID_W * GRID_H, 0);
	for(int x = 0; x < GRID_W; x++)
		tiles[(size_t)FLOOR_TY * GRID_W + x] = TILE_SOLID; // floor row
	for(int y = 10; y < FLOOR_TY; y++)
		tiles[(size_t)y * GRID_W + WALL_TX] = TILE_SOLID; // wall column
	g_Collision.InitTestGrid(GRID_W, GRID_H, tiles.data());
}

static const float T = 32.0f; // tile size px

// ---- per-tick input --------------------------------------------------------
struct In
{
	int dir = 0;
	bool jump = false;
	bool hook = false;
	int tx = 0, ty = -1; // aim target, relative to tee
};

struct TickRow
{
	int px, py, vx, vy, hookState, hookTick;
};

struct CoreOut
{
	int id;
	float ix, iy, ivx, ivy; // initial state
	std::vector<TickRow> rows;
	std::vector<In> inputs; // recorded per tick
};

struct Scenario
{
	std::string name;
	std::string desc;
	int ticks;
	std::vector<CoreOut> cores;
};

// ---- JSON emit (minimal) ---------------------------------------------------
static void emitScenario(bool first, const Scenario &s)
{
	if(!first)
		printf(",\n");
	printf("    {\n");
	printf("      \"name\": \"%s\",\n", s.name.c_str());
	printf("      \"desc\": \"%s\",\n", s.desc.c_str());
	printf("      \"ticks\": %d,\n", s.ticks);
	printf("      \"cores\": [\n");
	for(size_t ci = 0; ci < s.cores.size(); ci++)
	{
		const CoreOut &co = s.cores[ci];
		printf("        {\n");
		printf("          \"id\": %d,\n", co.id);
		printf("          \"init\": {\"x\": %.9g, \"y\": %.9g, \"vx\": %.9g, \"vy\": %.9g},\n",
			co.ix, co.iy, co.ivx, co.ivy);
		printf("          \"inputs\": [\n");
		for(size_t t = 0; t < co.inputs.size(); t++)
		{
			const In &in = co.inputs[t];
			printf("            {\"dir\": %d, \"jump\": %d, \"hook\": %d, \"tx\": %d, \"ty\": %d}%s\n",
				in.dir, in.jump ? 1 : 0, in.hook ? 1 : 0, in.tx, in.ty,
				(t + 1 < co.inputs.size()) ? "," : "");
		}
		printf("          ],\n");
		printf("          \"vectors\": [\n");
		for(size_t t = 0; t < co.rows.size(); t++)
		{
			const TickRow &r = co.rows[t];
			printf("            {\"tick\": %zu, \"px\": %d, \"py\": %d, \"vx\": %d, \"vy\": %d, \"hookState\": %d, \"hookTick\": %d}%s\n",
				t, r.px, r.py, r.vx, r.vy, r.hookState, r.hookTick,
				(t + 1 < co.rows.size()) ? "," : "");
		}
		printf("          ]\n");
		printf("        }%s\n", (ci + 1 < s.cores.size()) ? "," : "");
	}
	printf("      ]\n");
	printf("    }");
}

struct Core
{
	CCharacterCore c;
	int id;
};

static void makeCores(CWorldCore &world, CTeamsCore &teams,
	std::vector<Core> &cores, int n)
{
	cores.resize(n);
	for(int i = 0; i < n; i++)
	{
		cores[i].id = i;
		cores[i].c.Init(&world, &g_Collision, &teams);
		cores[i].c.Reset();
		cores[i].c.m_Id = i;
		world.m_apCharacters[i] = &cores[i].c;
	}
}

template <typename InputFn>
static Scenario run(const std::string &name, const std::string &desc, int ticks,
	std::vector<Core> &cores, const std::vector<int> &recordCores, InputFn inputFn)
{
	Scenario s;
	s.name = name;
	s.desc = desc;
	s.ticks = ticks;
	s.cores.resize(recordCores.size());
	for(size_t k = 0; k < recordCores.size(); k++)
	{
		CCharacterCore &c = cores[recordCores[k]].c;
		s.cores[k].id = recordCores[k];
		s.cores[k].ix = c.m_Pos.x;
		s.cores[k].iy = c.m_Pos.y;
		s.cores[k].ivx = c.m_Vel.x;
		s.cores[k].ivy = c.m_Vel.y;
	}

	for(int t = 0; t < ticks; t++)
	{
		for(size_t i = 0; i < cores.size(); i++)
		{
			In in = inputFn((int)i, t);
			cores[i].c.m_Input.m_Direction = in.dir;
			cores[i].c.m_Input.m_Jump = in.jump ? 1 : 0;
			cores[i].c.m_Input.m_Hook = in.hook ? 1 : 0;
			cores[i].c.m_Input.m_TargetX = in.tx;
			cores[i].c.m_Input.m_TargetY = in.ty;
		}
		for(auto &co : cores)
			co.c.Tick(true);
		for(auto &co : cores)
			co.c.Move();
		for(auto &co : cores)
			co.c.Quantize();

		for(size_t k = 0; k < recordCores.size(); k++)
		{
			CCharacterCore &c = cores[recordCores[k]].c;
			TickRow r;
			r.px = round_to_int(c.m_Pos.x);
			r.py = round_to_int(c.m_Pos.y);
			r.vx = round_to_int(c.m_Vel.x * 256.0f);
			r.vy = round_to_int(c.m_Vel.y * 256.0f);
			r.hookState = c.m_HookState;
			r.hookTick = c.m_HookTick;
			s.cores[k].rows.push_back(r);
			s.cores[k].inputs.push_back(inputFn(recordCores[k], t));
		}
	}
	return s;
}

int main()
{
	buildGrid();
	std::vector<Scenario> scenarios;
	const int TICKS = 30;

	const float floorY = FLOOR_TY * T - 19.0f; // ~1261, feet probe lands on floor
	const float airX = 5.0f * T; // 160, clear of wall

	// 1. free-fall + velocity ramp.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2(airX, 5.0f * T);
		scenarios.push_back(run("free_fall", "gravity + VelocityRamp, no input", TICKS,
			cores, {0}, [](int, int) { In in; return in; }));
	}

	// 2. ground move + friction.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2(airX, floorY);
		scenarios.push_back(run("ground_move", "grounded: accel right 15t then release (friction)", TICKS,
			cores, {0}, [](int, int t) { In in; in.dir = (t < 15) ? 1 : 0; return in; }));
	}

	// 3. air control.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2(airX, 5.0f * T);
		scenarios.push_back(run("air_control", "airborne: hold right (AirControl)", TICKS,
			cores, {0}, [](int, int) { In in; in.dir = 1; return in; }));
	}

	// 4. jump.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2(airX, floorY);
		scenarios.push_back(run("jump", "ground jump (edge), then air jump (release+press)", TICKS,
			cores, {0}, [](int, int t) {
				In in;
				if(t == 1 || t == 2 || t == 3)
					in.jump = true;
				else if(t == 6 || t == 7)
					in.jump = true;
				return in;
			}));
	}

	// 5. hook fly then retract (no target).
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2(airX, 20.0f * T);
		scenarios.push_back(run("hook_fly_retract", "hook fired up into open air -> length cap -> retract", TICKS,
			cores, {0}, [](int, int t) {
				In in;
				in.tx = 0;
				in.ty = -100;
				in.hook = (t >= 1);
				return in;
			}));
	}

	// 6. hook grab wall.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2((WALL_TX - 4) * T, 20.0f * T);
		scenarios.push_back(run("hook_grab_wall", "hook fired right into solid wall -> grab -> drag", TICKS,
			cores, {0}, [](int, int t) {
				In in;
				in.tx = 100;
				in.ty = 0;
				in.hook = (t >= 1);
				return in;
			}));
	}

	// 7. wall collision.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 1);
		cores[0].c.m_Pos = vec2((WALL_TX - 2) * T, 20.0f * T);
		scenarios.push_back(run("wall_collision", "drive right into wall (MoveBox x zeroed)", TICKS,
			cores, {0}, [](int, int) { In in; in.dir = 1; return in; }));
	}

	// 8. tee<->tee collision.
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 2);
		cores[0].c.m_Pos = vec2(airX, 20.0f * T);
		cores[1].c.m_Pos = vec2(airX + 10.0f, 20.0f * T);
		scenarios.push_back(run("tee_tee_collision", "two cores within PhysSize*1.25 -> push apart", TICKS,
			cores, {0, 1}, [](int, int) { In in; return in; }));
	}

	// 9. hook drag (player attach).
	{
		CWorldCore world;
		CTeamsCore teams;
		std::vector<Core> cores;
		makeCores(world, teams, cores, 2);
		cores[0].c.m_Pos = vec2(airX, 20.0f * T);
		cores[1].c.m_Pos = vec2(airX + 80.0f, 20.0f * T);
		scenarios.push_back(run("hook_drag", "core0 hooks core1 (player attach) and drags", 40,
			cores, {0, 1}, [](int i, int t) {
				In in;
				if(i == 0)
				{
					in.tx = 100;
					in.ty = 0;
					in.hook = (t >= 1);
				}
				return in;
			}));
	}

	printf("{\n");
	printf("  \"source\": \"ddnet@c7d760d5a CCharacterCore (gamecore.cpp)\",\n");
	printf("  \"quantization\": \"px=round_to_int(pos), vx=round_to_int(vel*256)\",\n");
	printf("  \"grid\": {\"w\": %d, \"h\": %d, \"floorTileY\": %d, \"wallTileX\": %d, \"tileSize\": 32},\n",
		GRID_W, GRID_H, FLOOR_TY, WALL_TX);
	printf("  \"scenarios\": [\n");
	for(size_t i = 0; i < scenarios.size(); i++)
		emitScenario(i == 0, scenarios[i]);
	printf("\n  ]\n");
	printf("}\n");
	return 0;
}
