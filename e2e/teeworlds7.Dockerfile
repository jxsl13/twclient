# Vanilla teeworlds 0.7.5 dedicated server for the e2e harness, BUILT FROM SOURCE
# with the Debug build enabled (SPEC V116 / T143).
#
# Why source, not the release tarball: `dbg_dummies` is CONF_DEBUG-gated, so the
# release binary REJECTS it and snapshots carry only the connecting client's own
# tee (B15.5). teeworlds' CMake defines CONF_DEBUG for the Debug config, so a
# Debug build registers `dbg_dummies` → bot characters → multi-character snapshots
# (restores V114), on top of the CTF flags/pickups from the ctf1 map.

# ---- builder: compile teeworlds_srv (server target, debug) -----------------
FROM debian:bookworm AS builder

ARG TW_REF=0.7.5

# Server-only build: with -DCLIENT=OFF (below) teeworlds' CMake skips the client
# deps (OpenGL/X11/SDL2/Freetype) entirely, so the builder needs only the
# toolchain + python3 (datasrc codegen). teeworlds bundles its game libs in-tree
# (src/engine/external).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git cmake ninja-build g++ python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# --recurse-submodules is REQUIRED: the stock maps (datasrc/maps, incl. ctf1) and
# other build inputs are git submodules; without them CMake configure aborts
# ("Missing datasrc/maps submodule"). Some submodules are pinned to dead `git://`
# URLs (GitHub removed the unauthenticated git protocol in 2022) — rewrite them
# to https so the recursive clone succeeds.
RUN git config --global url."https://github.com/".insteadOf "git://github.com/" \
    && git clone --depth 1 --branch "${TW_REF}" \
        --recurse-submodules --shallow-submodules \
        https://github.com/teeworlds/teeworlds.git .

# -DCLIENT=OFF = the documented server-only build (skips OpenGL/X11/SDL/Freetype).
# Debug config defines CONF_DEBUG (the gate that registers dbg_dummies).
RUN cmake -S . -B build -G Ninja -DCMAKE_BUILD_TYPE=Debug -DCLIENT=OFF \
    && cmake --build build --target teeworlds_srv \
    && test -x build/teeworlds_srv

# ---- runtime: slim image with the server binary + data tree ----------------
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/teeworlds
# CMake STAGES the full runtime data tree (maps incl. ctf1 from the datasrc/maps
# submodule, etc.) into build/data — the source `data/` dir does NOT carry the
# .map files, so copy the BUILT data tree (else "failed to load map ctf1").
COPY --from=builder /src/build/teeworlds_srv ./teeworlds_srv
COPY --from=builder /src/build/data ./data

COPY teeworlds7.cfg /opt/teeworlds/teeworlds7.cfg

EXPOSE 8303/udp

# teeworlds runs EACH CLI arg as a SEPARATE console command, so the whole
# `exec teeworlds7.cfg` must be ONE argument (else it splits into `exec` + an
# unknown command). The cfg sets ctf1 + gametype + dbg_dummies 4.
CMD ["./teeworlds_srv", "exec teeworlds7.cfg"]
