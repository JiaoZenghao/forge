# Verification record

## Windows CI path assertions — 2026-09-13

GitHub Actions run `34754729218` reproduced two test assertion failures on
Windows with both Go 1.25.x and stable: the uv argument expectation hardcoded
Unix separators, and the runner test compared short/long directory spellings
as strings. The assertions now use a native relative path and `os.SameFile`
directory identity respectively. Environment and stream assertions remain exact;
production code and the CI matrix are unchanged.

Local verification passed on macOS arm64, Go 1.27.0:

```text
go test -count=1 ./internal/generator ./internal/runner
go test -count=1 ./...
go test -race -count=1 ./internal/runner
go vet ./...
```

These local checks do not establish Windows runtime success; the fix must also
pass the existing Windows GitHub Actions jobs after push.

## Taskfile verification — 2026-09-08

The subsequent `task clean` addition was verified in a temporary fixture with
a separate writable `GOCACHE` (the default cache hit sandbox permissions).
Invoking it from a subdirectory removed `dist/`, `.task/`, `coverage.out`,
`forge`, and `forge.exe`; a second invocation succeeded with missing artifacts.
Unrelated files and source files remained intact, and a symlink at `dist` was
removed without touching its external target. `go vet ./tools/clean` passed;
the helper built locally and cross-compiled for Windows/Linux amd64. Native
Windows/Linux cleanup was not run. Existing workspace build artifacts were
preserved during this isolated verification.

Verified on macOS arm64 with Go 1.27.0 and Task 3.53.1:

- `task` listed all development tasks.
- `task run -- version --json` and `dist/forge version --json` succeeded.
- `task run` from `internal/cli` found the root Taskfile and displayed CLI help.
- `task build` produced the local CGO-disabled binary in `dist/`.
- `task check` passed all tests and vet (most test packages used Go's cache).
- `task test -- -v -count=1 ./internal/runner` passed without test-result caching.
- `task test:race -- -count=1 ./...` passed using a fresh temporary `GOCACHE`
  outside the sandbox so HTTP fixtures could bind loopback ports. The initial
  default-cache run failed with a Go package-resolution error; a fresh cache
  removed that error, then sandbox port restrictions required the outside run.
- Dry runs checked dependency download, formatting, release argument forwarding,
  and the Windows `dist/forge.exe` output path via `GOOS=windows task --dry build`.

No six-platform release build, Windows native execution, Docker execution, or
hosted CI run was performed for this Taskfile change. Earlier results follow.

Verified on 2026-09-06, macOS arm64, Go 1.27.0. These are executed results,
not assurances that unexecuted hosted environments have passed.

## Core and distribution

Passed:

```text
go test -race ./...
go vet ./...
go run ./tools/release --version v0.1.0 --commit unknown --date 2026-09-06T00:00:00Z --output dist
```

The test suite includes subprocess streams/cwd/env/exit/cancellation, Windows
batch quoting, strict metadata and unsafe paths, JSON argument failures,
dependency/version checks, malicious ZIPs, Initializr API fixtures, lock drift,
existing/racing destination preservation, failure cleanup, and all 42 nonempty
component × CI-provider × Docker combinations.

Six CGO-disabled targets were built and packaged:

- darwin/amd64 and darwin/arm64 (`.tar.gz`)
- linux/amd64 and linux/arm64 (`.tar.gz`)
- windows/amd64 and windows/arm64 (`.zip`)

Every SHA-256 entry in `dist/SHA256SUMS` was independently recomputed. Archive
contents were checked to contain exactly `forge` or `forge.exe`. The extracted
macOS arm64 binary executed successfully and reported `version: v0.1.0`,
`commit: unknown`, and `date: 2026-09-06T00:00:00Z` in JSON. Commit is unknown
because this supplied workspace is not a Git repository.

## Real ecosystem smoke checks

Next.js:

- Ran Forge against official `create-next-app@latest`, npm installation included.
- Generated Next.js 16.3.4 with npm lockfile and standalone output.
- `npm run lint` passed.
- `NEXT_TELEMETRY_DISABLED=1 npm run build` passed, including TypeScript checking,
  production compilation, and static page generation.
- `forge describe --json` and `forge validate --json` passed for that project.

Spring Boot:

- Downloaded a fresh Maven/web/Java 21 starter from official Spring Initializr.
- Current v2.3 metadata resolved Spring Boot 4.1.1 without a legacy suffix.
- Used an already-installed Temurin Java 21.0.3 and Maven 3.9.8.
- `mvn --batch-mode -Dmaven.repo.local=/tmp/forge-m2 verify` passed and produced
  the executable `target/verified-backend-0.0.1-SNAPSHOT.jar`.
- Generated GitLab configuration and Forge structural validation passed.

Python:

- Forge invoked actual uv 0.8.9 to initialize and lock the Python 3.12 application.
- `uv run --locked main.py` passed using CPython 3.12.14 and printed the generated
  application's greeting.
- Dockerfile generation and structural checks passed.

All smoke projects and dependency caches were isolated under temporary paths.
No generated repository was published or pushed.

## CI verification

Template tests decode rendered YAML and check component paths, first-branch
rules, default-branch registry guards, backend artifact needs, and Docker
variants. Independent actionlint 1.7.12 checks passed for all six generated
GitHub workflow variants and this repository's two GitHub workflows. Shellcheck
and pyflakes integration were disabled for that check; it validates workflow
syntax/expressions, not actual hosted execution.

## Review fixes verified

Regression tests and independent review covered corrections for:

- Next.js standalone insertion accidentally targeting a type import.
- Legacy Initializr API metadata translating modern versions into `.RELEASE`.
- Accepted project names yielding Java keyword package segments.
- JSON intent lost during argument parsing errors.
- Java launcher banners mistaken for the runtime version.
- npm development dependency and removed dependency lockfile drift.
- Windows `.cmd` quoting, cancellation pipe waits, and extensionless test binaries.
- Release version strings escaping the archive output directory.
- Backend JAR artifact/dockerignore consistency and Python interpreter alignment.

The reusable Forge skill was reviewed against creation, ordinary business work,
existing destinations, and optional missing dependency scenarios.

## Not executed here

- Native Windows or Linux runtime tests. CI is configured for Ubuntu, macOS,
  and Windows on Go 1.25.x and stable, including native Windows wrapper tests.
- Docker image builds and container startup: the local Docker daemon is not
  running. Dockerfiles were structurally tested and independently reviewed.
- Hosted GitLab/GitHub pipeline execution or registry pushes. Actual runner
  security profiles and registry permissions need the target CI environment.
- Signed/notarized releases or remote publication. Only local archives exist.

Known platform limitation: Windows cancellation bounds waits but does not use
Job objects to guarantee termination of every descendant process. Hard kills
and crashes may leave a reservation or staging directory; inspect before cleanup.
