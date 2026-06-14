# e2e — Docker game-server harness (SPEC T132 / V114)

A reproducible end-to-end harness for the `github.com/jxsl13/twclient` Go
client. `docker compose` brings up two **bot-populated** game servers; a
build-tagged Go test then drives the high-level `net6`/`net7` `Session` against
them and asserts `login + at least one non-empty snapshot`.

## What comes up

| service      | image / build                          | host port  | protocols | gametype | bots            |
|--------------|----------------------------------------|------------|-----------|----------|-----------------|
| `ddnet`      | vanilla DDNet `20.0` release (built)   | `8303/udp` | 0.6 + 0.7 | DDRace   | `dbg_dummies 4` |
| `teeworlds7` | vanilla teeworlds `0.7.5` (built)      | `8307/udp` | 0.7       | `ctf`    | `dbg_dummies 4` |

* The **ddnet** service is the **official vanilla DDNet** server (downloaded
  release tarball, like teeworlds7 — there is no public `ddnet/ddnet` image),
  run as a *sixup* server: it serves both teeworlds 0.6 and 0.7 clients on the
  same UDP port. Vanilla DDNet runs the **DDRace** gametype (no classic CTF), so
  its snapshots carry characters + DDRace entities but no CTF flags. The
  `dbg_dummies` bots make every snapshot multi-character — enough to validate
  character/player/score decode on BOTH protocols; CTF flag/pickup coverage
  comes from the `teeworlds7` service.
* The **teeworlds7** service is vanilla 0.7.5 on the stock `ctf1` map, so its
  snapshots carry the two flags + pickups on top of the bot characters.

### Why bots (`dbg_dummies`)

`dbg_dummies N` spawns `N` server-side dummy characters that join and move.
That guarantees every snapshot contains multiple `Character` objects (plus, on a
CTF map, the flag/pickup entities) without needing real clients — exactly the
rich snapshot the decoder needs to exercise. `sv_max_clients` is held at `16`
(well above the dummy count) because spawning more dummies than the client cap
segfaults the server ([teeworlds#1735](https://github.com/teeworlds/teeworlds/issues/1735)).

## Run it

Prerequisite: Docker + the compose plugin. Both upstream artifacts are
amd64-only (`platform: linux/amd64`); on Apple Silicon they run under emulation.

```sh
# from the repo root
docker compose -f e2e/docker-compose.yml up -d --build
# wait a few seconds for the servers to load their map + spawn dummies

TW_E2E=1 \
TW_E2E_DDNET_06=127.0.0.1:8303 \
TW_E2E_DDNET_07=127.0.0.1:8303 \
TW_E2E_VANILLA_07=127.0.0.1:8307 \
go test -tags e2e -run TestE2E ./e2e/ -v

docker compose -f e2e/docker-compose.yml down
```

Set `TW_DEBUG=1` for verbose client-side protocol logging.

### Test gating / skip behaviour

* The whole package is behind the `e2e` build tag, so it never compiles into the
  normal `go test ./...` run.
* At runtime it also requires `TW_E2E=1`; otherwise every test **skips** cleanly.
* Each server has its own env var (`TW_E2E_DDNET_06`, `TW_E2E_DDNET_07`,
  `TW_E2E_VANILLA_07`). An unset var skips just that test, so you can run a
  subset.

### Make target

Add this to the repo-root `Makefile` (this directory does not edit it):

```make
.PHONY: e2e
e2e: ## bring up the docker harness and run the e2e client tests
	docker compose -f e2e/docker-compose.yml up -d --build
	TW_E2E=1 \
	TW_E2E_DDNET_06=127.0.0.1:8303 \
	TW_E2E_DDNET_07=127.0.0.1:8303 \
	TW_E2E_VANILLA_07=127.0.0.1:8307 \
	go test -tags e2e -run TestE2E ./e2e/ -v ; \
	status=$$? ; \
	docker compose -f e2e/docker-compose.yml down ; \
	exit $$status
```

## Status / caveats

* The Go side (`e2e_test.go`, `doc.go`) is verified: `gofmt`, `go vet -tags e2e`
  and `go build -tags e2e` all pass.
* The **Docker** side was authored without a running Docker daemon and has **not
  been built or `up`'d**. The pinned DDNet release
  (`.../ddnet/ddnet/releases/download/20.0/DDNet-20.0-linux_x86_64.tar.xz`)
  and the teeworlds release URL
  (`.../releases/download/0.7.5/teeworlds-0.7.5-linux_x86_64.tar.xz`) are the
  documented/standard artifacts but should be confirmed on the first
  `docker compose ... up --build`.
* `e2e_test.go` is a **scaffold** (login + non-empty snapshot). The per-object
  0.6-vs-0.7 parity assertions are **T137** — see the `TODO(T137)` markers.
