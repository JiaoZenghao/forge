# Forge

Forge orchestrates official Next.js, Spring Initializr, and uv generators, then
adds company-standard monorepo structure, independent CI, Dockerfiles, and agent
instructions. It is a Go CLI; users need the distributed binary, not Go.

## Quick start

Build from source with Go 1.25 or newer:

```text
go build -o forge ./cmd/forge
```

On Windows use `go build -o forge.exe ./cmd/forge`. Put the binary on your PATH.
Standalone release archives target macOS, Windows, and Linux on amd64 and arm64.
Check a downloaded archive against `SHA256SUMS` before installing it.

```text
forge doctor --json
forge create order-system --frontend nextjs --backend springboot --java 21 --ci gitlab --docker --non-interactive --json
forge describe --path order-system --json
forge validate --path order-system --json
```

`forge create NAME` opens a Huh terminal form when stdin and stderr are terminals.
Explicit selection flags are retained. Piped input, `--non-interactive`, and
`--json` all disable prompts and use the defaults below. Forge does not initialize
or publish a remote repository.

## Commands and defaults

| Command | Purpose |
| --- | --- |
| `create NAME` | Generate and validate a new project |
| `doctor` | Inspect tools and platform without installing anything |
| `describe` | Read project configuration from `forge.yaml` |
| `validate` | Perform offline, read-only structural checks |
| `version` | Show version, commit, and build date |

All commands accept `--json` and `--non-interactive`. `--help --json` returns a
JSON help object. `describe`, `validate`, and `doctor` accept `--path` (default `.`).
They inspect that directory, not an automatically discovered ancestor.

| Create/doctor flag | Default | Values |
| --- | --- | --- |
| `--frontend` | `nextjs` | `nextjs`, `none` |
| `--backend` | `springboot` | `springboot`, `none` |
| `--java` | `21` | `21` in V1 |
| `--python` | `none` | `uv`, `none` |
| `--ci` | `gitlab` | `gitlab`, `github`, `none` |
| `--docker` | `true` | Use `--docker=false` to omit containers/image jobs |
| `--output` (create only) | `.` | Existing **parent directory**, not final project path |

The name is a lowercase slug of up to 63 characters, beginning with a letter.
Windows reserved names and filesystem/shell syntax are rejected. At least one
component is required. Java 21 is the only supported Java configuration in V1.

Examples:

```text
forge create web-only --backend none --ci github --docker=false --non-interactive
forge create ai-tool --frontend none --backend none --python uv --non-interactive
forge create orders --output "C:\Work Projects" --non-interactive --json
forge doctor --frontend none --backend springboot --json
```

## Tools and network

Creation requires only tools it invokes:

- Next.js: Node 22+, npm/npx 10+, and npm registry access.
- Spring Boot: HTTPS access to `start.spring.io`; local Java/Maven are not needed
  to download the starter. Building it requires Java 21 and Maven (or its wrapper).
- Python: uv and access to its configured package/interpreter sources. The initial
  service targets Python 3.12 and uv can obtain a compatible interpreter.
- Docker is optional locally, including when generating Dockerfiles. A working
  Docker engine is required if you later build images locally.

`doctor` inside a project checks its build tool requirements, including Java and
Maven for the backend. Outside a project, it inventories all tools; missing
optional tools do not fail the command. Supply selection flags to check a planned
project. A version probe is not a Docker daemon connectivity check.

Forge invokes `npx create-next-app@latest`, resolves a stable Spring Boot release
from official Initializr metadata, and invokes `uv init`/`uv lock`. It records
resolved framework versions in metadata when available. Initial creation therefore
requires network access and is not byte-for-byte reproducible across dates.
Committed npm and uv lockfiles and the Maven POM control subsequent builds.
Upstream generator drift is surfaced as a generation/validation failure.

## Generated projects

```text
order-system/
  frontend/                  # Next.js, npm lockfile, optional Dockerfile
  backend/                   # Java 21, Maven wrappers, optional Dockerfile
  services/ai/               # only with --python uv
  .gitlab/frontend-ci.yml     # with GitLab and frontend selected
  .gitlab/backend-ci.yml      # with GitLab and backend selected
  .gitlab-ci.yml
  docs/
  scripts/
  deploy/
  forge.yaml
  AGENTS.md
  README.md
```

GitHub selection instead creates component workflows under `.github/workflows/`.
The initial uv application is a command-line `main.py`, not a running web server;
add the business service or API framework afterward.

CI tests/builds each selected component independently. Frontend-only changes do
not build the backend, and vice versa. Changes to a component's CI file rerun
that component. Shared GitLab root CI changes may rerun all components. GitLab
rules distinguish merge requests, ordinary pushes, first default-branch commits,
and new feature branches. Docs-only changes do not build applications.

Frontend tests run `npm ci`, lint, and optional npm tests. With containers enabled,
the Docker build performs the production Next.js build once. Without containers,
CI runs a separate `npm run build`. Backend CI tests, packages with tests skipped,
then transfers `target/*.jar` to the image job without compiling it again.
Python CI checks Python syntax independently; add application tests as business logic grows.

GitLab image jobs use rootless BuildKit, CI registry credentials, and immutable
commit SHA tags. Rootless BuildKit still requires a compatible runner/security
profile; see [GitLab's runner requirements](https://docs.gitlab.com/ci/docker/using_buildkit/).
No Docker-in-Docker service is generated. GitHub uses Buildx and GHCR. Image pushes
are guarded; pull requests do not publish. Registry permissions and runner setup
remain the responsibility of your CI administrators.

## Safety and ownership

Forge refuses **every existing destination**, including empty directories and
symlinks. Generation takes place in a private sibling staging directory after
preflight checks, under a per-name reservation. Company files are created
exclusively. Validation must pass before native no-replace atomic publication.
A competing destination created during generation is preserved.

Normal failures remove staging and release the reservation. A hard kill, host
crash, or Windows descendant that outlives cancellation can leave a `.forge-*`
staging directory or reservation. Inspect it and confirm no create process is
running before removing it. Unsupported filesystem atomic-rename behavior fails
safely; Forge does not fall back to overwriting or partial copying.

`forge.yaml` is strict schema version 1 metadata. It records component paths,
frameworks, configuration, generator versions, and Forge-owned files. Component
paths use lowercase directory segments containing letters, numbers, underscores,
and hyphens. No absolute paths, nested component overlaps, or escaping symlinks.

V1 has no upgrade/add/update command. Forge never updates an existing project.
If intentionally customizing a Forge-owned file, inspect its purpose and validate
and test the change; ownership metadata is not an automatic merge mechanism.
Ecosystem source belongs to the application. Initial Next.js standalone output,
lint script, and Node baseline are documented Forge adjustments.

## Machine contract

JSON mode writes exactly one JSON object to stdout. Prompts, child-process logs,
and diagnostics use stderr. Parse stdout independently from stderr.

```json
{"status":"error","code":"MISSING_DEPENDENCY","dependency":"node","message":"..."}
```

Success responses use `status: "success"`. Create returns `path`, `project`, and
`validation`; describe returns project fields; doctor returns `platform` and
`tools`; validate returns `checks`. Check statuses are `ok` or `error`.
Failures include `code` and `message`, with relevant path/dependency/command
information. Validation failures retain the full checks array.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Dependency, compatibility, generation, project, validation, or runtime failure |
| 2 | Invalid CLI arguments |

Stable codes include `INVALID_ARGUMENT`, `MISSING_DEPENDENCY`,
`UNSUPPORTED_VERSION`, `PROJECT_ALREADY_EXISTS`, `INVALID_PROJECT`,
`COMMAND_FAILED`, `GENERATION_FAILED`, `VALIDATION_FAILED`, and `CANCELED`.

Structural validation does not execute application code, resolve dependencies,
prove arbitrary TypeScript configuration semantics, or validate hosted CI
credentials. Run component tests/builds after business-code changes.

## Development

With Go 1.25+ and [Task v3](https://taskfile.dev/docs/installation) installed,
use the root `Taskfile.yml` for common development commands:

```text
task                         # List available tasks
task deps                    # Download Go dependencies
task run                     # Show CLI help
task run -- version --json   # Pass arguments to Forge
task build                   # Build dist/forge (dist/forge.exe on Windows)
task clean                   # Remove local build artifacts and Task cache
task test                    # Run all tests
task test -- -v -count=1 ./internal/runner
task test:race                # Run all tests with the race detector
task vet                     # Run static analysis
task fmt                     # Format Go source files
task check                   # Run all tests, then static analysis
task release -- --version v0.1.0
```

`run`, `build`, `test`, `test:race`, `vet`, and `release` accept arguments after
`--`. For `test`, `test:race`, and `vet`, supplied arguments replace the default
`./...`; include `./...` when adding flags for all packages, for example
`task test -- -count=1 ./...`. Builds disable CGO; race tests use Go's normal
CGO settings. Release archives and `SHA256SUMS` default to `dist/`; pass
`--output`, `--commit`, or `--date` through to the existing release tool as needed.

`task clean` removes `dist/`, `.task/`, `coverage.out`, and the root `forge` /
`forge.exe` binaries. Missing artifacts are harmless. It preserves shared Go
build/test caches and module downloads; custom release output directories must
be cleaned separately.

Task is optional; the equivalent Go commands remain available:

```text
go test ./...
go test -race ./...
go vet ./...
go run ./tools/release -version v0.1.0
```

The HTTP integration fixtures need permission to bind loopback ports. Tests mock
network/external scaffolding boundaries, while exercising real filesystem,
metadata, template rendering, process execution, and CLI output. Hosted CI runs
Windows/macOS/Linux tests, including Windows `.cmd` execution. Cross-compilation
alone is not a substitute for those runtime tests.

See [architecture](docs/architecture.md), [verification](docs/verification.md),
and the reusable [Forge agent skill](skills/forge/SKILL.md).
