#!/usr/bin/env bash
set -euo pipefail

readonly CONFIG_PATH="${1:-/etc/llama-stack/config.toml}"

if ((EUID != 0)); then
    printf 'Run this script as root.\n' >&2
    exit 1
fi

read_toml() {
    local key=$1

    python3 - "$CONFIG_PATH" "$key" <<'PY'
import sys
import tomllib

with open(sys.argv[1], "rb") as config_file:
    value = tomllib.load(config_file)

for component in sys.argv[2].split("."):
    value = value[component]

if isinstance(value, bool):
    print(str(value).lower())
elif isinstance(value, list):
    print("\n".join(value))
else:
    print(value)
PY
}

cuda_compiler() {
    local candidate

    for candidate in \
        "${CUDA_HOME:-}/bin/nvcc" \
        /usr/local/cuda/bin/nvcc \
        /usr/local/cuda-*/bin/nvcc; do
        if [[ -n $candidate && -x $candidate ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    command -v nvcc 2>/dev/null
}

if [[ $(read_toml gpu.enabled) != true ]]; then
    printf 'GPU setup disabled in %s.\n' "$CONFIG_PATH"
    exit 0
fi

if command -v nvidia-smi >/dev/null && nvidia-smi >/dev/null 2>&1 && cuda_compiler >/dev/null; then
    printf 'NVIDIA driver and CUDA compiler are already operational.\n'
    exit 0
fi

if mokutil --sb-state 2>/dev/null | grep -q 'SecureBoot enabled' &&
    { ! command -v nvidia-smi >/dev/null || ! nvidia-smi >/dev/null 2>&1; }; then
    printf 'Secure Boot is enabled and no working signed NVIDIA driver is loaded.\n' >&2
    printf 'Disable Secure Boot or enroll a module-signing key, then rerun setup.\n' >&2
    exit 1
fi

REPOSITORY_URL="$(read_toml gpu.repository_url)"
readonly REPOSITORY_URL
mapfile -t driver_packages < <(read_toml gpu.driver_packages)
mapfile -t toolkit_packages < <(read_toml gpu.toolkit_packages)

curl --fail --location --show-error \
    "$REPOSITORY_URL" \
    --output /etc/yum.repos.d/cuda-fedora44.repo

dnf install -y "${driver_packages[@]}" "${toolkit_packages[@]}"

if ! cuda_compiler >/dev/null; then
    printf 'CUDA packages installed, but nvcc was not found under /usr/local/cuda.\n' >&2
    printf 'Check: dnf list installed "cuda-toolkit*" && find /usr/local -name nvcc\n' >&2
    exit 1
fi

printf '\nGPU packages installed. A reboot may be required before nvidia-smi succeeds.\n'
