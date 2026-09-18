# llama-stack

A setup and feature stack for llama.cpp. It bundles Open WebUI and other services to create a rich experience with LLMs on limited hardware.

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

- llama.cpp
- Open WebUI
- Podman Quadlets to give each "user" their own container in which the models may work.
- An exports service with a downloads web server to allow users to download files or artifacts from the workspaces that the models generated.
- SearXNG for web searches through a Podman Quadlet.
- A few MCP servers
