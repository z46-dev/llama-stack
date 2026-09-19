# llama-stack

A reproducible Fedora stack for running llama.cpp as a local AI service. It
combines native CUDA inference with Open WebUI, SearXNG, isolated Podman
workspaces, generated artifact downloads, and a small orchestration service.

## Target/Restrictions

This setup stack is designed to run on a Fedora 44 server. In my homelab, this is a VM on a Proxmox server. The GPUs I use are done through PCI passthrough, so the VM has direct access to the GPU.

For specs, a good minimum for a smooth experience is:

- 8 CPU cores
- 24GB of host RAM (separate from GPU memory)
- 1TB of storage (SSD preferred, HDDs work fine)
- One or more NVIDIA GPUs with at least 16GB of VRAM combined
- A stable internet connection for downloading models and updates

In my homelab setup, I have two NVIDIA A2 GPUs and a T4 for a total of 48GB of VRAM. Thus, the builds in this repo are optimized for that setup. If you have different GPU architectures, I would recommend checking the llama.cpp documentation for compatibility and performance considerations and modify the build scripts accordingly.

## Features

- Native CUDA llama.cpp server and OpenAI-compatible API
- Open WebUI with automatic context compaction enabled by default
- Podman Quadlets to give each "user" their own container in which the models may work.
- A read-only exports service for generated artifacts
- SearXNG for web searches through a Podman Quadlet.
- A read-only SearXNG MCP server and an isolated command-execution toolbox
- Persistent, capability-scoped TCP port leasing for agent workspaces
- An MCP resource server with allocate, list, renew, and release tools
- A Go CLI, host doctor, and orchestration daemon foundation

## Lifecycle

The intended ownership boundary is:

1. The administrator creates the Fedora 44 VM and optionally enrolls it in
   FreeIPA.
2. `llama-stack` installs the NVIDIA/CUDA dependencies, builds llama.cpp,
   installs every service, and runs its initial doctor checks.
3. The administrator adds models through `llama-stack` and configures network
   access, Open WebUI accounts, and optional external identity.

## Initial installation

Install the two bootstrap tools, clone the repository, inspect
`config/config.example.toml`, and run the setup target:

```bash
sudo dnf install -y git make
git clone https://github.com/z46-dev/llama-stack.git
cd llama-stack
sudo make setup
```

The setup is intentionally non-interactive. Existing configuration and secrets
are preserved. If the NVIDIA driver was newly installed and is not yet active,
the installer stops after rendering the stack and asks for a reboot.
If Secure Boot is enabled without an already working signed NVIDIA module,
setup stops before changing the GPU installation and explains the required
manual action.
CUDA discovery does not depend on login-shell profile files: setup locates
`nvcc` beneath `/usr/local/cuda`, passes the toolkit explicitly to CMake, and
uses a fresh CMake cache so a failed first configuration can be retried.
Setup also publishes `nvcc` through `/usr/local/bin` and registers the installed
llama.cpp and CUDA library directories with the dynamic linker.
The toolbox image is built in the `llama-stack` service account's rootless
Podman storage so the unprivileged inference service can launch it.

For a staged first installation that does not start services:

```bash
sudo make setup SETUP_FLAGS=--no-start
```

The installer supports IPA users: it records the invoking sudo user in the
local `llama-stack-admins` group. The non-secret configuration is readable by
the service and read-only CLI users; credentials remain under the restricted
`/etc/llama-stack/secrets` directory.

After rebooting:

```bash
sudo systemctl enable --now llama-stack.target
sudo llama-stack doctor
```

The default web endpoints are:

- `http://HOST:8080` — llama.cpp native UI and OpenAI-compatible API
- `http://HOST:8083` — Open WebUI
- `http://HOST:8081` — generated artifact downloads

On a fresh Open WebUI database, its first account is the administrator. User
registration, approvals, connections, and model visibility are then managed
from **Settings > Admin**. Open WebUI data is persisted beneath
`/var/lib/llama-stack/open-webui`; do not remove that directory when updating
the container image.

Setup pre-pulls enabled service images before enabling their Quadlets. This
keeps the large initial Open WebUI download out of systemd's startup timeout;
the generated unit also allows 15 minutes for first-start migrations.

To install rebuilt binaries and regenerate services without package or
llama.cpp setup:

```bash
sudo make install
```

## Configuration

The source of truth is `/etc/llama-stack/config.toml`. The initial file is
copied from `config/config.example.toml` and is never overwritten by an update.

After changing it, regenerate the managed units:

```bash
sudo llama-stack render
sudo systemctl restart llama-stack.target
sudo llama-stack doctor
```

The default 65,536-token model context compacts at 48,000 tokens, retaining 40%
of the newest messages. This leaves room for output, system instructions,
memory, and tool results.

## Models

The first implementation supports atomically importing a GGUF already present
on the host into managed model storage:

```bash
sudo llama-stack model add /srv/models/model.gguf
llama-stack model list
```

Direct Hugging Face downloads and declarative model manifests are planned for
the next model-management pass.

For a small tool-capable smoke-test model, download Qwen2.5 7B Instruct Q4_K_M
and import it. Qwen2.5 uses a tool-call format supported natively by llama.cpp;
it does not need a compatibility template:

```bash
curl -fL --retry 5 --continue-at - \
  -o ~/Downloads/llama-models/Qwen2.5-7B-Instruct-Q4_K_M.gguf \
  https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf
sudo llama-stack model add ~/Downloads/llama-models/Qwen2.5-7B-Instruct-Q4_K_M.gguf
```

The default configuration enables Jinja chat templates, llama.cpp's built-in
tools, the rootless Podman toolbox runtime, and the read-only SearXNG MCP
server. Open WebUI users do not receive host shell access: command execution
occurs inside the toolbox container. Keep public signup disabled because every
approved user can ask the model to invoke the enabled tools.

Open WebUI does not inherit llama.cpp's internal tools. The stack therefore
runs `llama-agent-tools.service`, an authenticated OpenAPI gateway which Open
WebUI registers automatically. It provides `exec_shell_command`, which runs in
a fresh resource-limited toolbox container, and read-only `web_search` through
SearXNG. The gateway is reachable from the Open WebUI container through
`host.containers.internal`, requires a generated bearer key, and its port must
not be opened in firewalld.

In Open WebUI, select Qwen2.5, open the **Tools** menu for the chat, enable
**llama-stack tools**, and verify that it exposes only `exec_shell_command` and
`web_search`. Native function calling is the default and Open WebUI's large
built-in tool bundle is disabled by default so smaller local models are not
overwhelmed with unrelated choices. Then test with:

```text
Use exec_shell_command exactly once to run:
printf 'TOOL_EXECUTION_CONFIRMED\n'; cat /etc/fedora-release; uname -m
Return only the actual tool output.
```

Test the gateway independently of the model or UI with:

```bash
KEY="$(sudo head -n1 /etc/llama-stack/secrets/agent-tools-api-key)"
curl -sS -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  --data '{"command":"cat /etc/fedora-release; uname -m"}' \
  http://127.0.0.1:8091/v1/exec | jq
```

This direct check must succeed before troubleshooting model behavior. Open
WebUI stores administrator configuration in its database; an existing Open
WebUI data directory may retain its old empty tool-server list. A fresh install
uses the generated configuration automatically. Existing installations can
add `http://host.containers.internal:8091` with `/openapi.json` under Admin
Settings → External Tools.

## Agent port leases

`llama-stackd` owns the shared TCP port pool configured under `[ports]`. Ports
are leased globally rather than assigned permanently to a user. Each lease is
bound to a user, agent run, workspace, and broker-controlled target host, and
is persisted through `gosqlite` so active forwarding can be reconstructed
after a daemon restart. `gasket` records precise lease-expiration jobs in a
separate SQLite database; the broker's periodic reaper remains a recovery
safety net if a scheduled job is delayed.

An administrator or the future workspace controller creates a short-lived run
capability:

```bash
sudo llama-stack resource capability issue \
  --user user-a \
  --run run-123 \
  --workspace workspace-123 \
  --target-host 127.0.0.1 \
  --ttl 7200
```

The plaintext capability is returned once. Its SHA-256 digest, identity scope,
target host, and expiry are stored; agents cannot choose their identity or use
the broker as an arbitrary network proxy.

The capability can be passed to the included stdio MCP server:

```bash
export LLAMA_STACK_CAPABILITY_TOKEN='lsrc_...'
/usr/local/libexec/llama-stack/llama-stack-resource-mcp
```

It exposes four tools:

- `port_allocate` maps one or more leased host ports to workspace target ports.
- `port_list` reports leases belonging to the current run capability.
- `port_renew` extends a lease without exceeding the capability lifetime.
- `port_release` immediately returns ports to the global pool.

When the pool has insufficient capacity, allocation returns a structured
`resource_exhausted` error and a retry hint. An agent may request a bounded
wait with `wait_seconds`; it will resume when capacity changes or return the
same error when its wait expires. Per-run and per-user limits prevent one run
from retaining the entire pool. Expired leases are reaped automatically.

The MCP server is deliberately not added to llama-server's global static MCP
configuration. A single static capability would erase user isolation. The
workspace controller will launch one MCP process per run and inject that run's
capability when workspace orchestration is implemented.

The read-only search MCP is global because it carries no user capability and
accepts only search terms; its upstream endpoint is fixed by generated
configuration. It cannot fetch an arbitrary caller-supplied URL.

Administrators can inspect or revoke leases:

```bash
sudo llama-stack resource ports list
sudo llama-stack resource ports revoke lease_0123456789abcdef
sudo llama-stack resource capability revoke cap_0123456789abcdef
```

## Administrative access

Potentially privileged CLI commands require both:

- execution through `sudo`; and
- membership of the original sudo user in the local `llama-stack-admins`
  group.

Direct root execution is permitted for system services and recovery. The
installer creates the group and adds the user who invoked `sudo make setup`.
Read-only commands such as `doctor`, `model list`, and `version` do not require
administrative membership.

Examples:

```bash
llama-stack doctor
llama-stack model list
sudo llama-stack model add /srv/models/example.gguf
```

## Development

```bash
make fmt
make test
make lint
make build
make verify
```

The same `make verify` and `make build` paths run in GitHub Actions and can be
executed locally through `act`.

The Go services intentionally build on focused modules instead of duplicating
their functionality: `gosqlite` provides typed persistence, `gasket` provides
durable scheduled jobs, `golog` provides daemon logging, and `go-arg` provides
the nested command-line parser.

## Technical Details

- A `llama-stack` user is created to run the services. This user has no login shell and is not intended for interactive use.
- A `llama-stack` cli tool is provided and installed to `/usr/local/bin/llama-stack`. This tool is used to manage the services and containers.
- This stack allows you to:
  - Manage separate users through Open WebUI and what they can do
    - Quotas, maximum usage for their containers, tools they can use, models, etc.
  - Let each user get a podman container with a workspace in which the LLMs can execute arbitrary commands, generate code, and perform work
  - Perform searches through SearXNG and have the results available to the LLMs in their workspaces
  - Have a downloads server that allows users to download files generated by the LLMs in their workspaces

### System services

`llama-stack.target` groups the independently managed services. Native inference
runs as `llama-server.service`; the control-plane foundation runs as
`llama-stackd.service`; container services are generated from Quadlets. Systemd
retains responsibility for dependency ordering, restart policy, and logging.

The initial trusted infrastructure containers use system Quadlets. Arbitrary
model-executed workspace containers will run rootlessly under the dedicated
service account when per-user workspace orchestration is added; they must never
inherit the infrastructure containers' privilege level.

Open WebUI remains the account and conversation authority and can later delegate
authentication to an external OIDC provider. `llama-stackd` will map those
identities to workspace policy, asynchronous jobs, quotas, and artifacts rather
than implementing another password database.

`llama-stackd` also binds only ports that have active leases and forwards TCP
connections to the scoped workspace target. Releasing or expiring a lease
closes its listener. The configured pool may be permitted through `firewalld`;
ports without active leases have no listening socket.
