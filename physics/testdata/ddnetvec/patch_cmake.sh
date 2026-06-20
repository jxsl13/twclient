#!/usr/bin/env bash
# patch_cmake.sh — append a `physics_driver` executable target to DDNet's
# CMakeLists.txt that links the SAME object libraries the real server links
# (game-shared + engine-shared + rust-bridge-shared + base, via ${DEPS} /
# ${LIBS}). Modelled on the in-tree `testrunner` target (CMakeLists ~3300) so
# all CCharacterCore / CCollision / g_Config symbols resolve identically to a
# normal DDNet build. Build-only; never touches the shipped Go repo.
#
# The driver source is copied into the tree as src/physics_driver.cpp so the
# target's include dirs (src/, src/generated/) resolve <game/...> headers.
#
# Usage: patch_cmake.sh <ddnet-src-root> <driver.cpp path>
set -euo pipefail
ROOT="${1:?usage: patch_cmake.sh <ddnet-src-root> <driver.cpp>}"
DRIVER="${2:?driver.cpp path}"

cp "$DRIVER" "$ROOT/src/physics_driver.cpp"

cat >> "$ROOT/CMakeLists.txt" <<'EOF'

# ---- T197 golden-vector driver (build-only, EXCLUDE_FROM_ALL) --------------
# Links the same object libs as the server's testrunner so the real
# CCharacterCore/CCollision physics + g_Config are available. ${LIBS} carries
# rust_engine_shared + crypto/curl/sqlite/zlib + pthread; ${DEPS} the bundled
# json/md5/zlib objects.
add_executable(physics_driver EXCLUDE_FROM_ALL
  src/physics_driver.cpp
  $<TARGET_OBJECTS:engine-shared>
  $<TARGET_OBJECTS:game-shared>
  $<TARGET_OBJECTS:rust-bridge-shared>
  ${DEPS}
)
add_dependencies(physics_driver generate_protocol)
target_link_libraries(physics_driver ${LIBS})
# base/math.h + base/vmath.h use C++20 concepts (e.g. `Numeric`); the per-target
# settings loop sets CXX_STANDARD 20 (CMakeLists ~3907) but our target is
# appended after it, so set it explicitly.
set_property(TARGET physics_driver PROPERTY CXX_STANDARD 20)
set_property(TARGET physics_driver PROPERTY CXX_STANDARD_REQUIRED ON)
# This target is appended AFTER the per-target settings loop, so replicate the
# include dirs + Debug compile definitions the object libs were built with
# (CMakeLists per-target settings ~3934). _GLIBCXX_DEBUG MUST match or the
# std::set/std::vector ABI used by CCharacterCore mismatches at link/run.
target_include_directories(physics_driver PRIVATE ${PROJECT_BINARY_DIR}/src)
target_include_directories(physics_driver PRIVATE src)
target_include_directories(physics_driver PRIVATE src/rust-bridge)
target_include_directories(physics_driver SYSTEM PRIVATE ${CURL_INCLUDE_DIRS} ${SQLite3_INCLUDE_DIRS} ${ZLIB_INCLUDE_DIRS})
target_compile_definitions(physics_driver PRIVATE $<$<CONFIG:Debug>:CONF_DEBUG>)
target_compile_definitions(physics_driver PRIVATE _FILE_OFFSET_BITS=64)
if(CMAKE_BUILD_TYPE STREQUAL Debug)
  target_compile_definitions(physics_driver PRIVATE _GLIBCXX_DEBUG)
  target_compile_definitions(physics_driver PRIVATE _LIBCPP_DEBUG=1)
endif()
if(CRYPTO_FOUND)
  target_compile_definitions(physics_driver PRIVATE CONF_OPENSSL)
  target_include_directories(physics_driver SYSTEM PRIVATE ${CRYPTO_INCLUDE_DIRS})
endif()
EOF

echo "patched CMakeLists.txt + copied driver to src/physics_driver.cpp"
