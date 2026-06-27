# Hardware-accelerated H.264 decode

Vedetta decodes H.264 to run motion detection and capture snapshots. By default
it uses a bundled software decoder (OpenH264) so the shipped binary stays a
single static file with no external codec dependency. Hardware decode is
available where it adds no dependency (macOS) or as an opt-in build (Linux).

## Configuration

Select the backend with `codecs.hwaccel` in `config.yml`:

```yaml
codecs:
  hwaccel: auto   # auto | software | videotoolbox | vaapi | nvdec
```

| Value          | Behavior |
|----------------|----------|
| `auto` (default) | Bundled OpenH264 software decoder (same as `software`). |
| `software`     | Bundled OpenH264 software decoder. |
| `videotoolbox` | macOS VideoToolbox only. No software fallback. |
| `vaapi`        | Linux Intel/AMD only. No software fallback. Requires a hardware build (below). |
| `nvdec`        | Linux NVIDIA only. No software fallback. Requires a hardware build (below). |

### Why `auto` is software, not hardware

Detection runs on a small camera sub-stream at a low frame rate (e.g. 640x480 at
5fps), where H.264 decode is already cheap and is **not** a CPU bottleneck.
Measured on Apple Silicon, VideoToolbox is actually ~45% slower per frame than
the software decoder for that workload (~4.9ms vs ~3.3ms): the GPU readback and
NV12-to-planar repack cost more than the hardware decode saves on small frames.
In absolute terms both are negligible (~2% of one core per camera at 5fps).

So `auto` uses the proven software decoder by default. Force a hardware backend
only for atypical workloads where decode genuinely costs CPU - many cameras, or
full-resolution decode - and measure that it helps your setup.

An explicit backend is honored exactly (no software fallback), so a forced
hardware decoder that cannot initialize disables decode rather than silently
falling back. All five values are accepted by config validation on every build,
but `vaapi`/`nvdec` only function in a binary compiled with `-tags hwaccel`.

## macOS (VideoToolbox)

Nothing to install. VideoToolbox uses only system frameworks, so the standard
build includes it. On Apple Silicon, H.264 is always hardware-decoded.

## Linux (VA-API / NVDEC) - opt-in build

The Linux backends link `libavcodec`/`libva` (VA-API) or CUDA (NVDEC), which the
default build deliberately avoids. Build them in explicitly.

### VA-API (Intel / AMD)

Install the development libraries (Ubuntu helper: `contrib/setup-hwaccel-ubuntu.sh`):

```sh
sudo apt-get install -y pkg-config libavcodec-dev libavutil-dev libva-dev va-driver-all
make build-hwaccel        # builds ./build/vedetta-hwaccel with -tags hwaccel
```

`make build-hwaccel` builds with `-tags hwaccel` (both backends; see NVDEC below
for why NVDEC adds no build dependency).

Or use the prebuilt image (`linux/amd64`), passing through the render node:

```sh
docker run --device /dev/dri ghcr.io/rvben/vedetta:hwaccel -config /config/config.yml
```

`make docker-build-hwaccel` builds that image from `Dockerfile.hwaccel`.

### NVDEC (NVIDIA)

NVDEC is included in the same hardware build and image. It references only
libavutil symbols and lets libavutil load the NVIDIA driver (libcuda) at
runtime, so it needs **no CUDA toolkit to compile** - the same libavcodec/libva
dev libraries as VA-API. At runtime it needs the NVIDIA driver:

```sh
docker run --gpus all ghcr.io/rvben/vedetta:hwaccel -config /config/config.yml
```

(requires the NVIDIA Container Toolkit on the host). For a local build,
`make build-hwaccel` already enables it.

`contrib/detect-hwaccel.sh` reports which backends the current host can support.

## Verifying

At startup Vedetta logs the resolved preference and what it found, e.g.:

```
decode preference set hwaccel=auto hardware_available=[vaapi]
```

`hardware_available=[]` means no hardware backend was compiled in or detected, so
`auto` is using software decode.
