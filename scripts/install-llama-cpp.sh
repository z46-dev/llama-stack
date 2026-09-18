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

if isinstance(value, list):
    print(";".join(value))
else:
    print(value)
PY
}

find_cuda_root() {
    local candidate

    for candidate in "${CUDA_HOME:-}" /usr/local/cuda /usr/local/cuda-*; do
        if [[ -n $candidate && -x $candidate/bin/nvcc ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    return 1
}

REPOSITORY="$(read_toml llama.repository)"
readonly REPOSITORY
REVISION="$(read_toml llama.revision)"
readonly REVISION
SOURCE_DIRECTORY="$(read_toml llama.source_directory)"
readonly SOURCE_DIRECTORY
CUDA_ARCHITECTURES="$(read_toml gpu.cuda_architectures)"
readonly CUDA_ARCHITECTURES
CUDA_ROOT="$(find_cuda_root || true)"
readonly CUDA_ROOT

if [[ $REVISION == REPLACE_WITH_TESTED_COMMIT ]]; then
    printf 'Set llama.revision to a tested llama.cpp commit in %s.\n' "$CONFIG_PATH" >&2
    exit 1
fi

if [[ -z $CUDA_ROOT ]]; then
    printf 'CUDA toolkit not found. Expected nvcc under /usr/local/cuda/bin.\n' >&2
    printf 'Check: dnf list installed "cuda-toolkit*" && find /usr/local -name nvcc\n' >&2
    exit 1
fi

export PATH="$CUDA_ROOT/bin:$PATH"

ln -sfn "$CUDA_ROOT/bin/nvcc" /usr/local/bin/nvcc
printf 'export PATH=%q/bin:$PATH\n' "$CUDA_ROOT" > /etc/profile.d/llama-stack-cuda.sh
chmod 0644 /etc/profile.d/llama-stack-cuda.sh

if [[ ! -d "$SOURCE_DIRECTORY/.git" ]]; then
    git clone "$REPOSITORY" "$SOURCE_DIRECTORY"
fi

git -C "$SOURCE_DIRECTORY" fetch --tags origin
git -C "$SOURCE_DIRECTORY" checkout --detach "$REVISION"

cmake \
    --fresh \
    -S "$SOURCE_DIRECTORY" \
    -B "$SOURCE_DIRECTORY/build" \
    -G Ninja \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_CUDA_COMPILER="$CUDA_ROOT/bin/nvcc" \
    -DCMAKE_CUDA_ARCHITECTURES="$CUDA_ARCHITECTURES" \
    -DCUDAToolkit_ROOT="$CUDA_ROOT" \
    -DCMAKE_INSTALL_PREFIX=/usr/local \
    -DBUILD_SHARED_LIBS=ON \
    -DGGML_CUDA=ON \
    -DLLAMA_CURL=ON

cmake --build "$SOURCE_DIRECTORY/build" --parallel "$(nproc)"
cmake --install "$SOURCE_DIRECTORY/build"

{
    printf '/usr/local/lib64\n'
    if [[ -d $CUDA_ROOT/targets/x86_64-linux/lib ]]; then
        printf '%s/targets/x86_64-linux/lib\n' "$CUDA_ROOT"
    fi
} > /etc/ld.so.conf.d/llama-stack.conf
chmod 0644 /etc/ld.so.conf.d/llama-stack.conf
ldconfig

if nvidia-smi >/dev/null 2>&1; then
    /usr/local/bin/llama-server --list-devices
else
    printf 'llama.cpp built successfully; CUDA device validation is deferred until after reboot.\n'
fi
