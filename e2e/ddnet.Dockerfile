# Vanilla DDNet sixup server for the twclient e2e harness, BUILT FROM SOURCE with
# the debug build enabled (SPEC V116 / T142). Serves BOTH teeworlds 0.6 and 0.7
# clients on one UDP port (sixup).
#
# Why source, not the release tarball: `dbg_dummies` is CONF_DEBUG-gated, so the
# ddnet.org release binary REJECTS it ("No such command") and snapshots carry
# only the connecting client's own tee (B15.5). A `-DDEV=ON` build defines
# CONF_DEBUG → registers `dbg_dummies`, so the server spawns bot characters and
# every snapshot is genuinely multi-character (restores V114).

# ---- builder: compile DDNet-Server (server only, debug) --------------------
# DDNet (19.x) compiles Rust components, so the builder needs a current Rust +
# Cargo (CMakeLists:646 "You must install Rust and Cargo to compile DDNet").
# The official rust:bookworm image bundles a recent toolchain on Debian, onto
# which we add the C++ build deps.
FROM rust:bookworm AS builder

# Pin the source ref (matches the release line). Bump together with the runtime.
ARG DDNET_REF=19.8.2

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git cmake ninja-build g++ pkg-config \
        libcurl4-openssl-dev libssl-dev libsqlite3-dev zlib1g-dev libpng-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone --depth 1 --branch "${DDNET_REF}" https://github.com/ddnet/ddnet.git .

# CLIENT=OFF drops the SDL/freetype/opus client deps. dbg_dummies is registered
# under `#if defined(CONF_DEBUG)`, and CONF_DEBUG is defined ONLY for the Debug
# config (`$<$<CONFIG:Debug>:CONF_DEBUG>`), so CMAKE_BUILD_TYPE MUST be Debug —
# RelWithDebInfo silently omits dbg_dummies (B15.5). DEV=ON enables the dev maps.
RUN cmake -S . -B build -G Ninja \
        -DCLIENT=OFF -DDEV=ON \
        -DCMAKE_BUILD_TYPE=Debug \
        -DAUTOUPDATE=OFF -DVIDEORECORDER=OFF -DPREFER_BUNDLED_LIBS=OFF \
    && cmake --build build --target DDNet-Server \
    && test -x build/DDNet-Server

# Build the 0.6→0.7 map converter and produce a TINY sixup map for the loss test
# (T164). Stock vanilla maps (dm1, ~6 KB) ship only a 0.6 version in data/maps/;
# the sixup server needs a 0.7 map in data/maps7/ or it logs "couldn't load map
# maps7/<name>" and DISABLES 0.7 for it. Converting dm1 yields a small sixup map
# so the loss test downloads it in seconds at full 20%/20% loss, vs ~50s for the
# 1.3 MB default "Sunny Side Up". Separate RUN layer so the cached DDNet-Server
# build above stays valid. Run from /src so data/mapres resolves for embedding.
RUN cmake --build build --target map_convert_07 \
    && ./build/map_convert_07 "data/maps/dm1.map" "data/maps7/dm1.map" \
    && test -f "data/maps7/dm1.map"

# ---- runtime: slim image with just the server binary + data tree -----------
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates libstdc++6 libcurl4 libssl3 libsqlite3-0 libpng16-16 zlib1g \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/ddnet
# DDNet-Server resolves data/ relative to the binary / CWD; copy both.
COPY --from=builder /src/build/DDNet-Server ./DDNet-Server
COPY --from=builder /src/data ./data

EXPOSE 8303/udp

# DDNet/teeworlds run each CLI argument as a console command. sv_max_clients must
# stay safely above the dummy count (teeworlds#1735). dbg_dummies now EXISTS
# (CONF_DEBUG build) so 4 bots spawn → multi-character snapshots. sv_max_clients +
# sv_max_clients_per_ip are 64 (not 16): all e2e clients share ONE container IP and
# the live-coverage run (T169/T170) connects many in quick succession — closed
# slots linger until timeout, so tight caps caused "server full" CLOSEs.
# econ (ec_port + ec_password) = the out-of-band admin channel the e2e tests use
# to provoke error states (kick/ban/shutdown); sv_rcon_password exercises the
# client's own rcon (T152). Static creds — e2e only, never a real deployment.
#
# Connect-flood limits RAISED for the dense single-IP live suite: DDNet refuses
# with "Too many connections in a short time" once an IP exceeds sv_connlimit
# (default 5) within sv_connlimit_time (default 20s), and sv_van_conn_per_second
# (default 10) rate-limits vanilla CONNECTs — the e2e run trips both from one IP,
# which previously skipped tests (V120). 100/1s + antispoof off = effectively no
# limit for the harness. e2e only.
CMD ["./DDNet-Server", \
     "sv_name 'tw-e2e ddnet source-debug sixup'", \
     "sv_port 8303", \
     "sv_register 0", \
     "sv_max_clients 64", \
     "sv_max_clients_per_ip 64", \
     "sv_sixup 1", \
     "sv_connlimit 100", \
     "sv_connlimit_time 1", \
     "sv_van_conn_per_second 0", \
     "sv_map \"Sunny Side Up\"", \
     "sv_rcon_password twrcon", \
     "ec_bindaddr 0.0.0.0", \
     "ec_port 9303", \
     "ec_password tweecon", \
     "dbg_dummies 4"]
