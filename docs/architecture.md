# Architecture

Forge is an orchestrator, shipped as one CGO-free executable. Cobra handles CLI
parsing; Huh handles optional interactive choices. Services never depend on
terminal output formatting.

`cli` → `scaffold` → `generator` + `templates` + `validate`

`project` defines the strict versioned metadata contract. `runner` is the only
core subprocess adapter. `doctor` uses it to probe dependencies. `apperror`
classifies failures without requiring agents to parse prose.

## Creation transaction

1. Validate selections/name and canonicalize the existing output parent.
2. Refuse existing destinations and check the selected generator dependencies.
3. Exclusively create a per-name reservation and a sibling staging directory.
4. Run official generators with closed stdin and explicit noninteractive choices.
5. Render company files from embedded assets; write exclusively through `os.Root`.
6. Save metadata and validate generated structure.
7. Publish using Darwin `RENAME_EXCL`, Linux `RENAME_NOREPLACE`, or Windows
   `MoveFileEx` without replacement. Release the reservation.

Failures before publication remove only the owned staging directory. Native
filesystem errors propagate; there is no unsafe rename fallback. This is intended
for user-owned local project directories, not an adversarial shared filesystem.
An uncatchable crash can leave staging/lock files; no recovery command is shipped.

## Process boundaries

`runner.Spec` carries command, argv, cwd, environment overrides, stdin, and
output streams. `Runner.Run(context.Context, Spec)` supports injected external
boundaries in tests. Native programs receive literal argv. Windows npm/npx use
`.cmd` resolution and explicit, validated cmd.exe quoting; unsupported batch
metacharacters fail clearly instead of being interpreted. Paths with spaces and
parentheses are covered by Windows runtime tests. Unix cancellation terminates
an owned process group. WaitDelay bounds inherited pipe waits; Windows does not
currently use Job objects to guarantee descendant termination.

Spring Initializr uses current v2.3 metadata, bounded HTTPS requests and context cancellation. ZIP
extraction rejects path traversal, reserved names, links, duplicate portable
paths, oversized archives, and preexisting symlink parents. No HTTP request
executes downloaded source within Forge itself.

## Metadata and validation

Schema version 1 represents components as objects with explicit portable paths.
Future layouts can reuse this model; future technology support needs explicit
schema/validation and generation decisions. YAML decoding rejects unknown fields,
duplicate keys, extra documents, and unsafe configuration. V1 does not update
metadata schema versions automatically.

Validation checks expected files, regular-file/symlink boundaries, npm scripts
and dependency lock consistency, Next standalone output when containerized,
Maven parent/Java/plugin/wrapper configuration, uv project structure, Dockerfiles,
and CI YAML files. Its purpose is actionable structural feedback, not application
execution or full interpretation of arbitrary framework configuration code.

## Extension points

Add a Cobra command adapter for a new operation; put behavior in a service. New
component types should provide generator and validation rules and conditional
company templates. Any future upgrade/add operation must design file ownership,
conflict detection, and idempotency before writing existing files. No plugin
protocol, remote project creation, deployment, or Helm/Kubernetes engine exists
in V1.

## Official references checked during implementation

- [Next.js CLI](https://nextjs.org/docs/app/api-reference/cli/create-next-app)
- [Next.js standalone output](https://nextjs.org/docs/app/api-reference/config/next-config-js/output)
- [Spring Initializr API formats](https://docs.spring.io/initializr/docs/current/reference/html/)
- [uv project creation](https://docs.astral.sh/uv/concepts/projects/init/)
- [GitLab BuildKit](https://docs.gitlab.com/ci/docker/using_buildkit/)
- [GitLab CI YAML](https://docs.gitlab.com/ci/yaml/)
