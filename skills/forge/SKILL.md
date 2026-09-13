---
name: forge
description: Create or inspect company-standard monorepos using the Forge CLI, including Next.js, Spring Boot, optional uv services, Docker, and component CI.
---

# Operate Forge

When asked to create a company-standard project, use `forge` to orchestrate
official generators and company templates. Do not manually reconstruct the same
scaffolding. Existing business feature work does not require regenerating a repo.

Inspect the binary and environment:

```text
forge version --json
forge doctor --json
```

Outside a Forge project, doctor is an inventory: optional missing tools do not
fail. Pass selection flags to check a planned configuration, or run doctor from
an existing project. Creation checks only the dependencies its generators use.
Do not install tools or publish repositories merely because doctor reports them.

Create with explicit selections and machine mode:

```text
forge create order-system --frontend nextjs --backend springboot --java 21 --python none --ci gitlab --docker --non-interactive --json
```

`--output` specifies an existing parent directory; Forge creates `parent/name`.
It refuses an existing destination even if empty. Use `--docker=false` to disable
containers. V1 supports Java 21. At least one component must be selected.

After creation, or before working in an existing Forge repository:

```text
forge describe --path order-system --json
forge validate --path order-system --json
```

Use `describe` and `forge.yaml` to find component boundaries. Prefer metadata to
heuristic directory discovery. After structural changes, rerun `validate`; run
component tests/builds for business-code changes because structural checks do not
execute or prove application behavior.

Parse **stdout only** as JSON. Child logs and diagnostics are on stderr. Check
both the process exit code and `status`. Exit 2 means invalid arguments; exit 1
means an operational/project failure. Use `code`, `dependency`, `path`, and
validation `checks` to choose the next action. Do not retry a failed create
blindly against an existing directory or remove a reservation without confirming
no generator is running.

`managedFiles` identifies company-owned files; V1 has no add/upgrade/update
operation. Avoid manually replacing CI/Docker/agent standards when the task is
ordinary application work. When the user intentionally requests a standards
change, inspect the file, make the scoped change, and validate and test it.
Never treat ownership metadata as permission to overwrite user changes.
