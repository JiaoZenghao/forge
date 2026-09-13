# Forge V1 design

Status: approved by the user and implemented on 2026-09-06. See ../../verification.md for executed checks and limitations.

## Scope and source

Build a standalone Go CLI for company-standard monorepos, with first-class
Windows support and deterministic non-interactive behavior for coding agents.
Implement `create`, `doctor`, `describe`, `validate`, and `version` only.

The supplied requirements end at the start of section 25. Generated `AGENTS.md`
will document component boundaries, build/test commands, Forge validation,
and ownership of generated engineering files. No additional missing requirements
are assumed.

## Architecture decision

Use a modular monolith: one binary with small internal packages. This gives
testable boundaries without plugin discovery, runtime template downloads, or
cross-process protocols.

Alternatives considered:

1. Put all orchestration in Cobra command handlers: fewer packages initially,
   but makes subprocess testing, error contracts, and future commands harder.
2. Introduce a provider/plugin framework: extensible, but adds unnecessary
   versioning and distribution complexity for three known generators.

Dependencies: Cobra for commands, Huh for terminal forms, a maintained YAML
library for strict metadata decoding, and a terminal-detection library if needed.
Use Go's standard library for HTTP, ZIP extraction, process execution, templates,
filesystem operations, JSON, cancellation, and tests. Verify dependency versions
and current official generator interfaces before implementation.

Proposed structure:

```text
cmd/forge/main.go             process entrypoint and exit handling
internal/cli/                Cobra commands, prompts, output rendering
internal/project/            metadata, options, component model
internal/runner/             process specs, resolver, execution, errors
internal/doctor/             dependency detection and compatibility checks
internal/generator/          Next.js, Initializr, uv orchestration
internal/scaffold/           staging, company overlays, creation transaction
internal/templates/          embedded company templates
internal/validate/           structured project checks
internal/apperror/           stable error codes and exit classification
docs/                       architecture and operating documentation
skills/forge/SKILL.md        reusable agent operating instructions
.github/workflows/          CLI test, build, and release automation
```

## CLI contract

`create NAME` requires a lowercase project slug: letters, digits, and internal
hyphens; it must start with a letter. Reject Windows reserved names, path
separators, dot components, trailing dots/spaces, and shell metacharacters.
Names are never interpreted as paths or commands.

Defaults: `frontend=nextjs`, `backend=springboot`, `java=21`, `python=none`,
`ci=gitlab`, `docker=true`. Explicit `--docker=false` is supported.
V1 supports Java 21; other values return `UNSUPPORTED_VERSION` rather than
generating untested configuration. At least one application/service is required.

`--output DIRECTORY` names the parent directory; the final project is
`DIRECTORY/NAME`. The default parent is the current directory. The parent must
exist. Reject any existing final path, including empty directories and symlinks.

Prompt for unspecified choices only when stdin and stderr are terminals and
neither `--non-interactive` nor `--json` is set. Otherwise use documented defaults.
Never prompt in JSON mode. Prompts and child-process output go to stderr.
Child stdin is closed in non-interactive mode; commands use explicit flags and
non-interactive environment settings. All commands support `--json` and accept
`--non-interactive` consistently; read-only commands never prompt.

`describe`, `validate`, and `doctor` accept `--path DIRECTORY` (default `.`).
`doctor` uses metadata when available; outside a project it inventories all tools
without claiming that every absent optional tool is required. Selection flags
allow checking dependencies for a proposed create invocation.

JSON stdout contains exactly one JSON document, including argument, dependency,
generation, and validation failures. Centralized error handling prevents Cobra
usage text or subprocess logs from corrupting JSON output. Normal help remains
human-readable unless JSON is explicitly requested; in that case return a JSON
help object. Detect JSON intent even if argument parsing fails.

Success objects have `status: "success"` and command-specific fields. Doctor
additionally reports platform and per-tool installed, version, required, and
compatible values. Describe exposes project/component configuration. Validate
returns checks with name, status, path, and actionable message. Create returns
the absolute project path, configuration, and validation result.

Errors have `status: "error"`, `code`, and `message`, with optional `dependency`,
`path`, `command`, and `exitCode` fields. Do not include secret environment values.
Codes include `INVALID_ARGUMENT`, `MISSING_DEPENDENCY`, `UNSUPPORTED_VERSION`,
`PROJECT_ALREADY_EXISTS`, `INVALID_PROJECT`, `COMMAND_FAILED`,
`GENERATION_FAILED`, `VALIDATION_FAILED`, and `CANCELED`.
Exit codes: 0 success, 2 invalid CLI arguments/options, 1 other failures.

## Metadata and ownership

Use strict, versioned, human-readable `forge.yaml`:

```yaml
schemaVersion: 1
project: order-system
type: monorepo
frontend:
  path: frontend
  framework: nextjs
  packageManager: npm
backend:
  path: backend
  framework: springboot
  buildTool: maven
  java: 21
ci:
  provider: gitlab
container:
  enabled: true
```

Omit unselected component sections. Optional Python uses `python.path:
services/ai`, `framework: python`, and `packageManager: uv`. Store the generating
Forge version and resolved ecosystem versions when obtainable. Reject unknown
schema versions, malformed fields, duplicate keys, unsafe paths, and unsupported
component combinations. Component objects carry their paths so future layouts
can be introduced without embedding paths in every service.

Record the list of Forge-owned files in metadata and identify ownership in file
comments where the format permits. Ecosystem source remains ecosystem-owned;
document the initial Next.js configuration/package-script adjustments. V1 has
no update command and never replaces an existing project. Metadata is not an
authorization to overwrite files in a future command.

## Safe generation and process execution

Validate arguments, parent/target paths, and relevant dependencies before
generation. Use an exclusive destination reservation and a staging directory
on the same filesystem. Generate and validate in staging, publish only after
success, and remove only paths owned by this invocation on failure. Finalization
must reject competing target creation and avoid merge/copy-overwrite semantics.
Document any platform limitation discovered while implementing finalization.

The runner accepts a command, argv, working directory, environment overrides,
stdin/stdout/stderr, and context. Return structured process errors with command
identity and exit status. Centralize executable resolution; prefer `npm.cmd` and
`npx.cmd` on Windows. Treat Windows batch wrappers explicitly and test quoting
for spaces and metacharacters. Never assemble a user-controlled shell script.
Core filesystem work uses Go APIs. Cancellation terminates owned subprocesses
as far as the platform permits and reports a failure rather than success.

Next.js: invoke `npx create-next-app@latest` with explicit npm, TypeScript,
App Router, ESLint, source-layout, and non-interactive choices supported by the
current official interface. Ensure a committed lockfile, working lint command,
and standalone configuration. Remove generator-created nested Git metadata
only inside staging. Avoid a second dependency installation if the generator
already performed it.

Spring Boot: request an official Initializr Maven/Java 21 project over HTTPS.
Select a stable compatible Boot version from Initializr metadata, not a
hard-coded speculative version. Configure a valid derived Java package,
include web support, and retain Maven wrappers for Unix and Windows. Apply
timeouts, HTTP-status checks, archive size limits, and ZIP traversal/symlink
protection. Record the selected version.

Python: invoke `uv init` non-interactively in `services/ai`, create a lockfile,
and include component documentation and independent CI when selected.

Creation requires only tools used by the selected generators. Docker is not
required just to write Dockerfiles. Doctor additionally checks relevant build
tools, including Git, Java, Maven, Node, npm, npx, uv, and Docker, distinguishing
required build dependencies from optional local tools.

## Company templates and CI

Use `embed` plus `text/template`. Render only selected components. Generate
README, AGENTS.md, gitignore, component Docker ignore files, and preserved
docs/scripts/deploy directories. Do not implement Kubernetes, Helm charts,
Argo CD, scanning integrations, or remote repository creation in V1.

GitLab: path-gated component includes plus component job rules. Explicitly
handle merge-request versus branch change comparisons and first-commit behavior;
do not rely on an unconditional `changes` rule for non-push pipelines. Component
CI-file changes should rerun that component. Shared root CI changes may rerun
all components. Docs-only changes should not build applications.

Frontend tests install via `npm ci`, run lint, and run optional tests. With
Docker enabled, the image job performs the single production build. Without
Docker, a frontend build job runs `npm run build`. Backend tests run Maven test;
the build job packages with tests skipped and exports the application JAR;
the image job uses `needs` and downloads that artifact without recompilation.
Use lockfile/dependency-aware caches and immutable commit image tags.

GitLab image jobs use rootless BuildKit with registry authentication, restricted
credentials, and documented runner requirements. Image pushes are limited to
appropriate push pipelines; untrusted merge requests do not receive push
credentials. Each selected component has an independent image when Docker is on.

GitHub: separate workflows with component path filters for push and pull request,
independent test/build steps, artifact transfer for the backend image, and
commit-tagged GHCR images on authorized pushes. Pull requests test/build without
publishing images. Use minimal permissions and current official actions verified
during implementation.

Next.js Dockerfile has deps/builder/runner stages, standalone output, production
environment, port 3000, and non-root runtime. Spring Boot uses a Java 21 runtime,
non-root user, port 8080, and the already-built JAR. Python gets a minimal uv-based
runtime consistent with its generated entrypoint. No Dockerfiles/image jobs when
Docker is disabled.

## Validation and tests

Validation is offline and read-only. It checks strict metadata, safe component
paths, expected directories/files, npm scripts/dependencies/lockfile, standalone
configuration when required, Maven Java configuration and wrappers, Python
configuration/lockfile, Docker assets, and selected CI files. It reports missing
and inconsistent configuration with actionable checks. Structural validation
does not claim that arbitrary application code builds or CI executes successfully.

Test-first implementation covers:

- Flag/default parsing, invalid names/options, JSON-only output, and exit codes.
- Metadata round trips, invalid schemas, path traversal, and missing files.
- Runner working directories, env overrides, streams, failures, cancellation,
  Windows resolution, and wrapper argument quoting.
- Generator argument construction, Initializr HTTP failures, malicious ZIPs,
  and deterministic offline fixtures using an HTTP test server.
- Existing-target preservation, cleanup on generator/validation failure,
  destination races, and successful publication.
- Every frontend/backend/Python, CI-provider, and Docker selection combination.
- CI YAML structure, independent path rules, backend artifact dependencies,
  frontend production build placement, and no pushes from untrusted PRs.

Run unit/integration tests, `go vet`, race tests where supported, and binary CLI
smoke tests. Compile darwin, Windows, and Linux for amd64 and arm64 with CGO
disabled. Add hosted Windows/macOS/Linux test jobs; cross-compilation alone is
not evidence of successful Windows runtime behavior. Attempt official-generator
smoke tests when network and installed dependencies permit, and report any
unverified external execution explicitly.

## Delivery

Deliver source, embedded templates, tests, build/release workflow, usage and
architecture documentation, and `skills/forge/SKILL.md`. Release artifacts need
checksums and version/build metadata. End users install a binary and do not need
Go. No remote publishing or release creation is part of this request.
