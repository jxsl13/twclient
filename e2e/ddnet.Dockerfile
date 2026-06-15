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
# (CONF_DEBUG build) so 4 bots spawn → multi-character snapshots.
# econ (ec_port + ec_password) = the out-of-band admin channel the e2e tests use
# to provoke error states (kick/ban/shutdown); sv_rcon_password exercises the
# client's own rcon (T152). Static creds — e2e only, never a real deployment.
CMD ["./DDNet-Server", \
     "sv_name 'tw-e2e ddnet source-debug sixup'", \
     "sv_port 8303", \
     "sv_register 0", \
     "sv_max_clients 16", \
     "sv_max_clients_per_ip 16", \
     "sv_sixup 1", \
     "sv_map \"Sunny Side Up\"", \
     "sv_rcon_password twrcon", \
     "ec_bindaddr 0.0.0.0", \
     "ec_port 9303", \
     "ec_password tweecon", \
     "dbg_dummies 4"]
