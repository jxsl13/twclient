# Dockerfile — one-time golden per-tick physics-vector extractor (twclient T197).
#
# Compiles a tiny C++ driver against DDNet's REAL CCharacterCore physics
# (ddnet@c7d760d5a) and emits input->QUANTIZED-output fixtures as JSON on
# stdout. Mirrors the from-source e2e recipe (e2e/ddnet.Dockerfile, base
# rust:bookworm + cmake/ninja/g++) since gamecore.cpp needs the CMake-generated
# generated/protocol.h. The emitted JSON is GENERATED DATA (no GPL vendoring).
#
# Build + extract via run.sh (which does `docker build` then copies the JSON
# out of the image), so the deliverable physics/testdata/ddnet_vectors.json is
# reproducible from a pinned source rev.

FROM rust:bookworm AS builder

# Pin the EXACT source rev the spec names (V149/T197). ddnet master clone is
# checked out at this commit for the reference line numbers too.
ARG DDNET_REF=c7d760d5ad7fcdde596ca51ea8aa8f017a74fac6

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git cmake ninja-build g++ pkg-config python3 \
        libcurl4-openssl-dev libssl-dev libsqlite3-dev zlib1g-dev libpng-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone https://github.com/ddnet/ddnet.git . \
    && git checkout "${DDNET_REF}" \
    && git rev-parse HEAD

# Build-only patches: a public test-grid setter on CCollision + a CMake target
# for the driver. Copied in then applied.
COPY patch_collision.sh patch_cmake.sh driver.cpp /patches/
RUN bash /patches/patch_collision.sh /src \
    && bash /patches/patch_cmake.sh /src /patches/driver.cpp

# Configure exactly like the e2e server build (CLIENT=OFF, DEV=ON, Debug) so
# the codegen + game-shared/engine-shared object libs are produced, then build
# the driver target only.
RUN cmake -S . -B build -G Ninja \
        -DCLIENT=OFF -DDEV=ON \
        -DCMAKE_BUILD_TYPE=Debug \
        -DAUTOUPDATE=OFF -DVIDEORECORDER=OFF -DPREFER_BUNDLED_LIBS=OFF \
    && cmake --build build --target physics_driver \
    && test -x build/physics_driver

# Emit the golden vectors to a known path inside the image.
RUN ./build/physics_driver > /ddnet_vectors.json \
    && head -c 200 /ddnet_vectors.json \
    && wc -c /ddnet_vectors.json

# Minimal final stage so `docker build -o` / `docker create` can extract just
# the JSON artifact.
FROM scratch AS artifact
COPY --from=builder /ddnet_vectors.json /ddnet_vectors.json
