# Alocal

`Alocal` is a single-binary Go CLI that answers one question honestly:

> **What is the largest local LLM this machine can actually run well?**

It detects the hardware, turns it into a transparent **Hardware Score (0–100)**, scores an embedded catalog of 97 local models, applies a **hard memory-fit filter**, and recommends the highest-scoring model that does not exceed the machine's capability. It can then download a **GGUF from Hugging Face** and run it locally with a **bundled llama.cpp runtime** — no Ollama installation and no background server to manage. The interactive chat streams tokens, renders Markdown, and opens a `Ctrl+X` command palette for models, downloads, MCP servers and settings. Every number is computed by a documented formula, and every score can be inspected with `--verbose` or `--json`.

It is a planning tool, not a benchmark. Tokens/sec values are memory-bandwidth **estimates** with an explicit ±35% range. The chat is a conversational TUI (it does not edit files or run tools). `alocal models` adds an interactive catalogue of 425 real Hugging Face GGUF repositories, with categories, search, hardware-fit explanations and a resumable download workflow.

```
curl -fsSL https://raw.githubusercontent.com/N1XNAC/alocal/main/install.sh | ALOCAL_REPO=N1XNAC/alocal sh
alocal                  # opens the chat UI
alocal scan
alocal recommend
```

Bare `alocal` opens the chat UI in an interactive terminal; `alocal --help` lists every command.

---

## Quick start

Prerequisites: **Go 1.22+** to build from source. Nothing else is required: Alocal downloads GGUFs from Hugging Face and fetches a small llama.cpp runtime on first use.

### One-line install

The installer verifies the published SHA-256 checksum before installing to `~/.local/bin`.

```bash
curl -fsSL https://raw.githubusercontent.com/N1XNAC/alocal/main/install.sh | ALOCAL_REPO=N1XNAC/alocal sh
export PATH="$HOME/.local/bin:$PATH"          # add to ~/.bashrc or ~/.zshrc to persist
alocal --version
alocal                                      # opens chat
```

Windows (PowerShell):

```powershell
$env:ALOCAL_REPO = "N1XNAC/alocal"
irm https://raw.githubusercontent.com/N1XNAC/alocal/main/install.ps1 | iex
# Add %USERPROFILE%\.local\bin to PATH, then run: alocal
```

Options: `ALOCAL_VERSION=v0.2.0` pins a release, `ALOCAL_INSTALL_DIR=/path/to/bin` changes the destination, and `ALOCAL_BASE_URL=https://mirror/...` points at a mirror. To publish releases yourself, push a `v*` tag: `.github/workflows/release.yml` builds all six static binaries plus `checksums.txt`.

### Update Alocal

The installer remembers the release source, so updating is one command:

```bash
alocal update --check          # report current and latest version
alocal update                  # download, verify, and replace the binary
alocal update --tag v0.2.0 --yes
```

`alocal update --repo owner/repo`, `ALOCAL_REPO`, `ALOCAL_BASE_URL`, or `~/.config/alocal/update.json` can supply the source if it was not saved at install time. The new binary is checked against the release's `checksums.txt` before replacing the running executable. On Windows the previous binary is kept as `alocal.exe.old` until you restart and delete it.

### From a cloned checkout

```bash
git clone <your-fork> alocal && cd alocal
./install.sh                      # builds from source with Go 1.22+
export PATH="$HOME/.local/bin:$PATH"
# or: make build && make install

alocal scan                       # hardware report + Hardware Score
alocal recommend                  # best model for this machine
alocal recommend --task coding    # prefer coding models
alocal explain qwen2.5-7b-instruct-q4_k_m
alocal models                       # interactive catalogue (TTY); filters stay script-friendly
alocal models --quant Q4_K_M --min-context 32768
alocal download qwen2.5-7b-instruct-q4_k_m   # downloads a GGUF from Hugging Face
alocal                            # opens chat with the best fitted/downloaded model
```

On Windows, build with `go build -o alocal.exe .\cmd\alocal` and put the `.exe` on PATH. `make cross` builds six static binaries (Linux/macOS/Windows × amd64/arm64) into `dist/`.

No Go is needed to run a prebuilt release binary. Detection degrades gracefully: if `nvidia-smi`, `rocm-smi`, or WMI is unavailable, `Alocal` records a warning and keeps going instead of failing.

### Interactive chat

Bare `alocal` opens a full-screen terminal UI. The startup screen draws a purple pixel hexagon with a geometric `A`, a pale-lavender pixel `Alocal` wordmark and a small purple dash, using only ANSI foreground colours so the terminal background stays transparent. It then shows the real version, a concise hardware summary, the local llama.cpp runtime state, the active model, and its context size. Hardware detection is time-bounded so the UI still appears quickly. In a non-interactive shell it prints help instead; `NO_COLOR=1` produces the same layout without ANSI codes.

```bash
alocal                                                  # opens chat; auto-selects a downloaded GGUF
alocal chat                                             # same UI, explicit command
alocal chat --auto                                      # recommend the best fit for this machine
alocal chat --auto --mock-hardware tests/fixtures/rtx4090.json
alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF --quant Q5_K_M
alocal chat bartowski/Qwen2.5-7B-Instruct-GGUF/model-Q5_K_M.gguf \
  --system "You are a concise coding assistant"
```

- `--auto` scans hardware, picks the best-fitting catalog model, and uses a downloaded GGUF from the same Hugging Face repo if available. Otherwise it prints the exact `alocal pull` command and opens the UI anyway so you can download from the palette.
- With no downloaded GGUF, `alocal` still starts. Press `Ctrl+X` → **Download GGUF from Hugging Face**, enter `owner/repo`, pick a file, and Alocal downloads, verifies, and loads it without restarting.
- Inference runs in a private `llama-server` started by Alocal on a free localhost port. Context length and GPU offload come from Alocal's fit calculation plus your settings; the KV cache is sized by the same formula used by `alocal explain`.
- Keys: `Enter` send, `Shift+Enter` newline, `Esc` stop generation, `Ctrl+C` / `Ctrl+D` quit, `Ctrl+X` command palette.
- Slash commands: `/help`, `/clear`, `/model <id>`, `/system <prompt>`, `/info`, `/models`, `/quit`.
- Requires an interactive terminal; piping stdin/stdout is rejected with a clear error.

#### `Ctrl+X` command palette

| Entry | What it does |
|---|---|
| Switch model | Pick any downloaded GGUF; Alocal restarts the local runtime with it. |
| Download GGUF from Hugging Face | Enter `owner/repo`, choose a `.gguf` file, watch progress, then auto-load it. |
| Scan hardware | Show the Hardware Score, tier, and usable memory inline. |
| Recommend a model | Show the top three models that fit this machine and their tok/s estimates. |
| Explain a model | Show weights + KV cache + overhead and the Hugging Face repo for a catalog id. |
| MCP servers | List configured servers; press `A` to add `name command [args...]`, `D` to delete. |
| Settings | Set `context gpu_layers temperature` (for example `8192 -1 0.7`). |
| Help / Quit | Key reference and exit. |

`↑`/`↓` (or `k`/`j`) navigate, typing filters, `Enter` selects, `Esc` goes back or closes the palette. MCP server definitions live in `~/.config/alocal/mcp.json`; they are configuration for future tool-enabled sessions, not active tool calls in this conversational TUI.

### Interactive model catalogue

`alocal models` opens a full-screen catalogue in an interactive terminal. The embedded snapshot in `data/discovery.json` contains **425 real Hugging Face repositories** generated by `scripts/gen_discovery.py`; one entry is one repository, so separate quantizations are shown as files inside that entry rather than counted as separate base models. A live Hugging Face search adds newer repositories on startup when the network is available. If the network fails, the embedded snapshot is used unchanged — no placeholder or invented models are shown.

Sections: **General Chat, Coding, Reasoning, Mathematics, Multilingual, Vision, Embeddings, Audio, Lightweight Models, Downloaded Models**.

| Key | Action |
|---|---|
| `↑`/`↓` or `j`/`k` | Move between models. |
| `←`/`→`, `h`/`l`, `Tab` | Switch category; `1`–`9` jump directly (`0` = Downloaded Models). |
| `/` | Search name, publisher, task, quant, license or repository. |
| `[` / `]` | Previous / next page. |
| `Enter` | Detailed model view. |
| `d` | Download workflow: Download model, Select quantization, View hardware requirements, Cancel. |
| `e` | Fetch real GGUF file names and byte sizes from the Hub tree API. |
| `r` | Toggle hardware requirements in the detail view. |
| `Esc` / `q` | Go back or quit. |

The detail view reports only published data: publisher, repository, base model/architecture when available, parameters (including active parameters for MoE models when published), real file size, context length, license, quantization list, tasks/pipeline features, community stats, and a memory-fit classification. Unknown fields are labelled `not published` rather than guessed.

Compatibility is computed from the local file size, selected quantization, requested context, KV-cache formula, runtime overhead, and the usable memory detected by `alocal scan` (discrete VRAM, Apple unified memory, or available system RAM). Results are **Recommended**, **Warning** (less than the 15% safety headroom), **Incompatible** (estimated need exceeds usable memory), or **Unknown** (no published size). Explanations are printed in the detail view; this is a fit estimate, never a speed guarantee based on parameter count.

Downloads stream to `~/.local/share/alocal/models/` with live progress. `Esc` cancels and keeps the `.part` file; the next attempt resumes with a `Range` request when the server supports it. The finished GGUF is checked for its magic bytes and byte count, registered in `models.json`, and appears under **Downloaded Models**. It can then be run with `alocal chat <id>`. Incompatible or unknown-fit downloads always require an explicit `y` confirmation; they are never started silently. Duplicate repositories are skipped, and network failures leave existing files untouched.

Piped output, `--json`, `--local`, and explicit `models` filters keep the original non-interactive table/JSON output for scripts. Set `ALOCAL_OFFLINE=1` to skip the optional live refresh.

#### Local files, runtime, and credentials

| Path / variable | Purpose |
|---|---|
| `~/.local/share/alocal/models/` | Downloaded GGUF files. |
| `~/.local/share/alocal/models.json` | Registry of downloaded models (id, repo, file, size, quant). |
| `~/.local/share/alocal/runtime/` | llama.cpp `llama-server` binary plus bundled shared libraries, fetched once. |
| `~/.config/alocal/settings.json` | Context, `-ngl` layers, temperature, system prompt, last model. |
| `~/.config/alocal/mcp.json` | MCP server definitions edited in the palette. |
| `~/.config/alocal/update.json` | Release source used by `alocal update`, written by the installers. |
| `ALOCAL_DATA_DIR`, `ALOCAL_CONFIG_DIR` | Override the directories above. |
| `ALOCAL_LLAMA_SERVER` | Use an existing `llama-server` binary instead of downloading one. |
| `ALOCAL_LOAD_TIMEOUT` | Seconds to wait for model loading before giving up (default `300`). Large GGUFs on slow disks may need more. |
| `ALOCAL_OFFLINE` | Set to `1` to skip the live Hugging Face refresh in `alocal models` and use the embedded snapshot. |
| `HF_ENDPOINT`, `HF_TOKEN` | Hugging Face mirror and token for private or gated repositories. |

No Ollama daemon is used. If `llama-server` is already on `PATH`, Alocal uses it; otherwise it fetches the matching static llama.cpp release for Linux/macOS/Windows on amd64 or arm64. Patch versions are pinned by the release asset it selects at first run.

The first launch can take a while: Alocal downloads the llama.cpp runtime once, then `llama-server` loads the GGUF into memory. The status line shows each stage (download, extraction, tensor loading, KV cache, offload) so it is clear that loading is progressing. If you have a slow disk or a very large model, raise `ALOCAL_LOAD_TIMEOUT` (for example `ALOCAL_LOAD_TIMEOUT=900 alocal`).

### Reproducible demos without owning the hardware

Every command accepts a mock profile, so the examples below can be reproduced on any machine:

```bash
./alocal scan      --mock-hardware tests/fixtures/rtx3060.json
./alocal recommend --mock-hardware tests/fixtures/rtx3060.json
```

A mock profile is flagged in both text (`MOCK PROFILE`) and JSON (`"mock": true`).

---

## Commands

| Command | Purpose |
|---|---|
| `alocal` | Open the interactive chat UI with auto model selection (TTY only; prints help otherwise). No model needed to start. |
| `alocal scan` | Detect CPU, RAM, GPU(s), disk, OS/container/WSL. Shows the Hardware Score breakdown. |
| `alocal score` | Print only the score (tab-separated in plain mode). `--verbose` shows components. |
| `alocal recommend` | Hard memory filter + nearest-model-score rule. Shows alternatives and reasons. |
| `alocal models` | Interactive catalogue of 425 real GGUF repositories in a TTY: categories, search, pagination, details, compatibility and downloads. Piped/`--json`/filtered calls keep the original catalog table (`--task`, `--family`, `--quant`, `--runtime`, `--min-context`, `--max-size`); `--local` lists downloaded GGUFs. |
| `alocal explain <id>` | Per-model fit: KV cache, overhead, required memory, score comparison, tok/s estimate. |
| `alocal download <id\|owner/repo>` | Download a GGUF from Hugging Face into `~/.local/share/alocal/models` with quant/file selection and GGUF verification. `alocal pull` and `alocal get` are aliases. |
| `alocal chat [model]` | Interactive streaming chat TUI with a local llama.cpp runtime. `--auto` selects the best model for this hardware. |
| `alocal update` | Check or install a newer release binary, verified against `checksums.txt`. |
| `alocal catalog validate` | Recompute every model score from the schema and report mismatches. `--file` validates an external file. |

Global flags:

```
--json             machine-readable output (stable field names)
--verbose          raw detection sources + score details
--mock-hardware F  load a mock profile from JSON, or "-" for stdin
```

`NO_COLOR=1` disables ANSI styling. When stdout is not a TTY, output is plain text.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Runtime/detection/data error (unreadable file, failed Hugging Face download, invalid GGUF, llama.cpp server failure) |
| `2` | Usage or invalid-input error (unknown flag/model/task, bad catalog, validation failure) |
| `3` | Valid query, but no ideal match (no model fits memory, or all fitting models exceed the hardware score). Output is still produced. |

---

## How the Hardware Score works

```
Hardware Score = 0.50 × memory + 0.25 × bandwidth + 0.20 × compute + 0.05 × disk
```

Each component is 0–100 and the score is the weighted sum, rounded to one decimal.

### Effective model memory (the hard limit)

| Machine | Effective memory |
|---|---|
| Discrete GPU(s) | `min(free VRAM, total VRAM × 0.95)` summed across GPUs |
| Apple Silicon (unified) | `total RAM × 0.70` |
| CPU-only | `available RAM` (if unknown: `total × 0.75`) |

A separate **15% safety reserve** is applied when matching: `safe_limit = effective_memory × 0.85`. It can be changed with `--safety-margin 0.01…0.5`; passing `0` falls back to the default 15%.

### Memory component

Piecewise-linear interpolation over required memory:

```
0.3 GB → 3    1 GB → 9     2 GB → 15    4 GB → 25     8 GB → 40
12 GB → 51    16 GB → 58   24 GB → 68   32 GB → 75    48 GB → 84
64 GB → 89    96 GB → 95   192 GB → 100
```

### Bandwidth component

Log-like curve, because 50→100 GB/s matters more than 900→1000:

```
25 GB/s → 12   50 → 25   100 → 40   200 → 54   400 → 72
600 → 82       800 → 88  1000 → 91  1600 → 96  2500 → 99
```

- Discrete GPU: table value; multiple GPUs use **fastest + 10% of the others**.
- Apple: unified-memory bandwidth from the GPU table.
- CPU-only: estimated from RAM type/speed (dual-channel assumption), e.g. DDR4-3200 → 51.2 GB/s.

### Compute component

- Discrete GPU: compute tier from `data/gpus.json` (multi-GPU: fastest + 5% of others).
- Apple GPU: Metal compute tier from the GPU table.
- CPU-only: `cores × GHz × SIMD factor`, mapped as `65 × sqrt(effort / 64)`, capped at 65.

SIMD factors: AVX-512 1.50, AVX2 1.25, NEON 1.20, SSE4.2 1.00, unknown 0.90.

### Disk component

NVMe 100, SSD 80, HDD 25, unknown 50. Free space caps the score: `<10 GB` → max 10, `<25 GB` → max 35.

### Tiers

| Score | Tier |
|---|---|
| < 30 | Entry |
| 30–49 | Mid |
| 50–69 | High |
| 70–84 | Enthusiast |
| ≥ 85 | Workstation |

---

## How the Model Score works

Each catalog entry stores the architecture needed to estimate its KV cache:

```
KV bytes/token = 2 (K and V) × layers × kv_heads × head_dim × 2 bytes
KV cache GB    = context_tokens × KV bytes/token ÷ 1e9
required GB    = (file_size_GB + KV cache GB) × 1.12 + 0.35
memory_score   = memory_need_curve(required GB)
demand_score   = demand_curve(active_params_B × bits_per_weight ÷ 8)
model_score    = 0.70 × memory_score + 0.30 × demand_score
```

- `1.12` covers KV growth, scratch buffers, and allocator overhead; `0.35 GB` is fixed backend/context overhead.
- Active parameters are used for MoE models, so a sparse 30B model is not punished like a dense one.
- Effective bits per weight: `Q3_K_M 3.90`, `Q4_K_M 4.85`, `Q5_K_M 5.70`, `Q6_K 6.60`, `Q8_0 8.50`, `FP16 16.0`.
- The `demand_curve` is a 0–100 curve over active weight bytes.

`alocal catalog validate` recomputes every score. The shipped catalog is generated by `scripts/gen_models.go`, which calls the same scoring code and refuses to write inconsistent data.

### Recommendation rule

1. Filter by requested `--task`, `--runtime`, and minimum supported `--context`.
2. Apply the hard memory filter: `required GB ≤ effective memory × (1 − safety margin)`. A model over the line is **never** recommended, no matter how good its score.
3. Among fitting models, choose the **highest Model Score that does not exceed the Hardware Score**. This avoids recommending a model the machine can technically load but cannot run comfortably.
4. Tie-breakers, in order: larger parameter count, newer release year, task match, then ID.
5. If every fitting model exceeds the Hardware Score → `over_budget` (exit 3), still shown with a clear warning.
6. If nothing fits → `no_fit` (exit 3), with concrete upgrade suggestions (more VRAM, enough unified RAM, smaller quantization).

`recommend` always includes safer (smaller) and stretch (larger or slower) alternatives.

### Tokens/sec estimate

```
tok/s ≈ effective bandwidth (GB/s) ÷ active weight GB × efficiency
active weight GB = active_params_B × bits_per_weight ÷ 8
```

Efficiency: discrete GPU 0.45, Apple GPU 0.42, CPU 0.30 × SIMD adjustment. Values are capped at 250 tok/s and shown with a ±35% range.

**This is a roofline estimate, not a benchmark.** Quantization kernels, backend, batch size, context length, and layer offload all change real performance.

---

## Three example machines

### 1. RTX 3060 12 GB + Ryzen 7 5800X + 32 GB

```console
$ alocal scan --mock-hardware tests/fixtures/rtx3060.json
alocal hardware scan
  MOCK PROFILE — values came from a JSON file, not this machine

System
  OS: linux fixture (x86_64)

CPU
  Model: AMD Ryzen 7 5800X 8-Core Processor
  Cores: 8 physical / 16 logical
  Clock: base 3.80 GHz / max 4.70 GHz
  Architecture: x86_64
  SIMD: AVX2, F16C, SSE4.2

Memory
  Total: 32.0 GB
  Available: 26.0 GB
  Type / speed: DDR4 / 3200 MT/s
  Est. bandwidth: 51.2 GB/s (theoretical dual-channel estimate)

GPU
  NVIDIA GeForce RTX 3060 (NVIDIA, discrete)
    VRAM: 12.0 GB total, 11.4 GB free
    Bandwidth: 360 GB/s · compute tier 63/100
    Driver: 555.99
    source: fixture

Disk
  Type: NVME
  Free: 500.0 GB of 1000.0 GB
  Mount: /
  Device: /dev/nvme0n1p2

Hardware score
  59.6 / 100  High  ·  effective memory 11.4 GB

Next: alocal recommend

$ alocal recommend --mock-hardware tests/fixtures/rtx3060.json --top 3
alocal recommendation
  Hardware: 59.6 / 100  High  ·  11.4 GB usable discrete VRAM
  Safe memory limit: 9.7 GB (15% reserve)
  Filters: context 8192

  Best fit: Yi 1.5 9B Chat (Q5_K_M). It needs 9.2 GB; your safe limit is 9.7 GB.

Recommended
MODEL                    QUANT   PARAMS  MODEL/HW     MEMORY  FITS  TOK/S EST
Yi 1.5 9B Chat           Q5_K_M  8.8B    43.0 / 59.6  9.2 GB  yes   25.7 (16.7–34.7)
Granite 3.1 8B Instruct  Q5_K_M  8.2B    40.9 / 59.6  8.4 GB  yes   27.9 (18.2–37.7)
Qwen3 8B                 Q5_K_M  8.2B    40.6 / 59.6  8.2 GB  yes   27.9 (18.2–37.7)
```

The 12 GB card cannot hold a comfortable 14B at 8K context, so `alocal` prefers a 9B at Q5 rather than a 14B at the edge of free VRAM.

### 2. MacBook Air M2, 8 GB unified memory

```console
$ alocal recommend --mock-hardware tests/fixtures/m2air8.json --top 3
alocal recommendation
  Hardware: 38.7 / 100  Mid  ·  5.6 GB usable unified memory
  Safe memory limit: 4.8 GB (15% reserve)

  Best fit: Qwen2.5 3B Instruct (Q8_0). It needs 4.4 GB; your safe limit is 4.8 GB.

Recommended
MODEL                  QUANT   PARAMS  MODEL/HW     MEMORY  FITS  TOK/S EST
Qwen2.5 3B Instruct    Q8_0    3.1B    27.3 / 38.7  4.4 GB  yes   12.7 (8.3–17.2)
Gemma 3 4B IT          Q4_K_M  4.3B    26.7 / 38.7  4.6 GB  yes   16.2 (10.5–21.8)
Qwen2.5 1.5B Instruct  FP16    1.5B    26.6 / 38.7  4.2 GB  yes   13.5 (8.8–18.3)
```

Only 70% of the 8 GB is treated as GPU-usable, so 4B-class models are the practical ceiling at 8K context.

### 3. CPU-only laptop, 16 GB RAM

```console
$ alocal recommend --mock-hardware tests/fixtures/cpu-only-16.json --top 3
alocal recommendation
  Hardware: 42.8 / 100  Mid  ·  11.5 GB usable system RAM
  Safe memory limit: 9.8 GB (15% reserve)

  Best fit: Granite 3.1 8B Instruct (Q5_K_M). It needs 8.4 GB; your safe limit is 9.8 GB.

Recommended
MODEL                    QUANT   PARAMS  MODEL/HW     MEMORY  FITS  TOK/S EST
Granite 3.1 8B Instruct  Q5_K_M  8.2B    40.9 / 42.8  8.4 GB  yes   2.6 (1.7–3.6)
Qwen3 8B                 Q5_K_M  8.2B    40.6 / 42.8  8.2 GB  yes   2.6 (1.7–3.6)
Llama 3.1 8B Instruct    Q5_K_M  8.0B    39.9 / 42.8  8.0 GB  yes   2.7 (1.8–3.6)
```

The model fits, but the estimated 2–3 tok/s is the honest answer: CPU inference is bandwidth-bound. The report calls this a "stretch pick" rather than pretending it will feel fast.

Two more fixtures are included: `rtx4090.json` (24 GB, 64 GB RAM) and `nogpu4.json` (4 GB RAM, deliberately no-fit for most of the catalog).

---

## Editing the data

Both data files are embedded into the binary at build time through `data/embed.go`.

### `data/gpus.json`

A lookup table used for VRAM, bandwidth, compute tier, and vendor fallbacks.

```json
{
  "pattern": "(?i)\\bRTX\\s?3060\\b",
  "vendor": "NVIDIA",
  "kind": "discrete",
  "vram_gb": 12,
  "bandwidth_gbps": 360,
  "compute_tier": 63
}
```

- `pattern` is a Go regular expression matched against the detected GPU name.
- `kind` is `discrete`, `integrated`, or `unified`.
- Per-vendor fallbacks are used when no pattern matches, so a new GPU is never scored as zero.
- Add a row, run `go test ./internal/gpu`, and rebuild. The table has ~100 entries.

### `data/models.json`

Each entry carries identity, sizing, architecture, tasks, license, and runtime references:

```json
{
  "id": "qwen2.5-7b-instruct-q4_k_m",
  "family": "Qwen2.5",
  "display_name": "Qwen2.5 7B Instruct",
  "params_b": 7.62,
  "active_params_b": 7.62,
  "quant": "Q4_K_M",
  "file_size_gb": 4.62,
  "max_context": 32768,
  "tasks": ["chat", "general"],
  "license": "Apache-2.0",
  "release_year": 2024,
  "arch": { "layers": 28, "kv_heads": 4, "head_dim": 128 },
  "runtimes": {
    "ollama": "qwen2.5:7b-instruct-q4_K_M",
    "llamacpp": "hf.co/bartowski/Qwen2.5-7B-Instruct-GGUF",
    "lmstudio": "lmstudio-community/Qwen2.5-7B-Instruct-GGUF"
  },
  "score_context": 8192,
  "model_score": 33.9
}
```

After editing, regenerate or validate:

```bash
go run ./scripts/gen_models.go     # regenerates models.json from base definitions
./alocal catalog validate          # recomputes scores and checks the schema
```

Validation checks unique IDs, lowercase slugs, known quantization/task/runtime labels, positive sizes, KV architecture fields, and that stored `model_score` matches the formula. CI fails on any mismatch. The `ollama` field is kept only as a cross-runtime reference; Alocal itself uses `llamacpp` (a Hugging Face GGUF repo) for downloads and inference.

---

## What is measured vs estimated

**Measured (from the OS):** CPU model, core counts, clocks, architecture, SIMD flags; total/available RAM and memory type/speed where the platform exposes it; GPU name and VRAM/free VRAM from `nvidia-smi`, `rocm-smi`, sysfs, macOS `system_profiler`, or Windows WMI; disk type, size, and free space; OS, kernel, container/WSL flags.

**Estimated:** GPU bandwidth and compute tier come from the table when the driver does not report them; CPU bandwidth is a dual-channel theoretical estimate; KV cache and runtime overhead are formula-based; tokens/sec is a roofline model.

**Known limitations**

- Apple unified memory is capped at 70% of RAM; real headroom depends on macOS and other apps.
- Multi-GPU VRAM is summed, but tensor-split scaling is not linear.
- Integrated GPUs are treated as low-capability and do not replace system RAM for fit decisions.
- Detectable-but-exotic GPUs may use a vendor fallback until added to `gpus.json`.
- Model scores assume the stated quantization/architecture metadata; wrong metadata produces wrong scores.
- Tokens/sec ignores prompt processing, batching, and backend-specific kernels.

The tool never claims a benchmark result. If a number is estimated, it says so next to the number.

---

## Development

```bash
make check             # gofmt + vet + race tests
make test              # go test ./...
make race              # go test -race ./...
make vet               # go vet ./...
make cross             # six static binaries into dist/
make smoke             # offline smoke tests using the fixtures

# Or with Go directly:
go test ./...          # unit + integration tests (scoring, catalog, matching, CLI, chat, HF, runtime)
go vet ./...
go test -race ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/alocal
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/alocal
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/alocal
```

Layout:

```
cmd/alocal/          entry point, panic recovery
internal/hardware/   detection per OS + mock loader + Detector interface
internal/gpu/        embedded GPU table, vendor matching, RAM bandwidth
internal/scoring/    curves, hardware score, model score, throughput
internal/catalog/    schema, validation, filters, runtime tags
internal/match/      hard memory filter + recommendation rule
internal/cli/        cobra commands, text/JSON rendering
internal/hf/         Hugging Face repo metadata, GGUF picker, verified downloader
internal/runtime/    llama.cpp asset installer, llama-server lifecycle, streaming API client
internal/local/      GGUF registry, settings, MCP config, update source, path helpers
internal/update/     self-updater: GitHub release lookup, checksum verification, atomic replace
internal/chat/       bubbletea chat TUI (block-glyph logo, streaming, Markdown, Ctrl+X palette)
data/                gpus.json, models.json, //go:embed
tests/fixtures/      five reproducible machine profiles
scripts/             catalog generator
install.sh/ps1       one-line installers (download + SHA-256 verification)
.github/workflows/   CI test/build matrix and tag-triggered release workflow
```

Mock profiles follow the same schema as `alocal scan --json`; a profile needs a CPU model plus RAM or GPU data to normalize. Tests cover the five fixtures, score formulas, catalog integrity, CLI exit codes, and JSON contracts.

CI (`.github/workflows/ci.yml`) runs formatting, installer syntax checks, vet, tests, and a build matrix for Linux/macOS/Windows on amd64 and arm64. Pushing a `v*` tag triggers `.github/workflows/release.yml`, which publishes the same six binaries and a `checksums.txt` used by `install.sh` / `install.ps1`.
