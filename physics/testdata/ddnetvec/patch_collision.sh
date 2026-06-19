#!/usr/bin/env bash
# patch_collision.sh — BUILD-ONLY patch that injects a public test-grid setter
# into DDNet's CCollision, so the golden-vector driver can fabricate a tile
# grid without loading a real .map / CLayers. Mirrors the e2e sed-injection
# style (cf. e2e/teeworlds7.Dockerfile flood-ban patch). Applied inside the
# Docker builder to the cloned ddnet@c7d760d5a tree; NEVER touches the shipped
# Go repo or DDNet source on disk.
#
# Usage: patch_collision.sh <ddnet-src-root>
set -euo pipefail
ROOT="${1:?usage: patch_collision.sh <ddnet-src-root>}"
H="$ROOT/src/game/collision.h"
C="$ROOT/src/game/collision.cpp"

# 1. Declare InitTestGrid right after "void Init(CLayers *pLayers);".
#    (sed inserts the declaration line after the match.)
sed -i 's#\(\s*void Init(CLayers \*pLayers);\)#\1\n\tvoid InitTestGrid(int Width, int Height, const unsigned char *pIndices);#' "$H"

# 2. Append the definition to collision.cpp. It allocates a fresh CTile grid,
#    copies the tile indices, and points m_pTiles/m_Width/m_Height at it. All
#    DDRace layer pointers stay null (set by Unload in the ctor), which every
#    collision query already null-guards.
cat >> "$C" <<'EOF'

// ---- TEST-ONLY (T197 golden-vector driver, build-only sed patch) ----------
void CCollision::InitTestGrid(int Width, int Height, const unsigned char *pIndices)
{
	Unload();
	m_Width = Width;
	m_Height = Height;
	CTile *pTiles = new CTile[(size_t)Width * Height];
	for(int i = 0; i < Width * Height; i++)
	{
		pTiles[i].m_Index = pIndices[i];
		pTiles[i].m_Flags = 0;
		pTiles[i].m_Skip = 0;
		pTiles[i].m_Reserved = 0;
	}
	m_pTiles = pTiles;
}
EOF

echo "patched: $H, $C"
