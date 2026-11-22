# Media processing

`internal/media` sniffs uploads, reads their size and duration, and makes thumbnails, posters and hover teasers. It also strips location data from JPEGs.

## What is generated

| Output | From | Format | When |
|---|---|---|---|
| Thumbnail | images | JPEG q82, or PNG if the image has transparency; at most 640 px wide | at upload, synchronously |
| Poster | video, animated images | JPEG q82, at most 640 px wide; frame at 10% of the duration (first frame under 2 s) | media job |
| Teaser | video, animated GIF/APNG/WebP | MP4, H.264 (libx264, CRF 30, veryfast), 24 fps, yuv420p, at most 480 px wide, no audio, `+faststart` | media job |

Nothing is upscaled. Thumbnails apply EXIF orientation and use the first frame of animations. Posters and teasers show transparent areas as black.

Teasers: clips of 8 s or longer get five 1.5 s segments from 10, 30, 50, 70 and 90% of the duration, joined with ffmpeg's concat filter (7.5 s total). Shorter clips are used whole. Output is capped at 8 s and at the byte limit (default 1 MiB); a larger result is deleted and the job fails. The encoder also gets a bitrate cap derived from the byte limit, so few clips reach it.

Sniffing uses magic bytes first, then `http.DetectContentType`. The filename extension only breaks ties between media types (for example `.m4a` versus `.mp4` for the same brand). Text is always `text/plain`, so a playlist (HLS, ffconcat, SDP) renamed to `.mp4` is never given to ffmpeg.

## Decoders

- In process (Go): JPEG, PNG, GIF, WebP (still and animated), BMP, TIFF. The header is checked against the pixel limit (default 100 million) before anything is decoded.
- ffmpeg: AVIF and HEIC/HEIF (including tile grids), all video, and audio probing. AVIF and HEIC are probed with ffprobe and checked against the pixel limit before decoding.
- Animated WebP teasers: ffmpeg 8.1 cannot decode animated WebP, so frames are composited in Go and piped to ffmpeg as raw RGBA.
- Not thumbnailed: ICO, JPEG XL, SVG (SVG is treated as text), PDF, audio cover art.

## Location stripping

`StripJPEGLocation` copies the JPEG byte for byte, without re-encoding pixels, except that it:

- drops every Exif APP1 segment, and if it had an orientation, writes a new Exif segment holding only that tag
- drops XMP and extended XMP (APP1), Photoshop/IPTC (APP13) and MPF (APP2)
- drops everything after the end-of-image marker (MPF secondary images, motion-photo videos)

Other Exif fields (camera, date, colour-space hints) are lost with the Exif segment. ICC profiles, JFIF and comments are kept. On malformed input it returns an error; keep the original in that case.

## How ffmpeg is constrained

Every ffmpeg and ffprobe call goes through one function, which:

- forces the demuxer from the sniffed type (`-f mov`, `matroska`, `avi`, `mpegts`, `mpeg`, `flv`, `asf`, `ogg`, `mp3`, `flac`, `wav`, `aac`, `aiff`, `gif` or `apng`) and sets `-format_whitelist` to that one demuxer. Playlist and script demuxers (hls, concat, sdp, image2, ...) cannot be selected.
- sets `-protocol_whitelist file`. For mov it also sets `-enable_drefs 0 -use_absolute_path 0`, which turns off external track references.
- passes inputs and outputs as absolute `file:` paths
- adds `-nostdin -hide_banner -loglevel error -nostats`
- bounds decoder, filter and encoder threads (default 2)
- runs the child with an empty environment and `/` as its working directory, in its own process group. The whole group gets SIGKILL on timeout or cancellation, and ffmpeg itself gets SIGKILL if the server dies.
- applies rlimits:
  - `RLIMIT_AS` (default 1 GiB)
  - `RLIMIT_CPU` (default 120 s)
  - `RLIMIT_FSIZE` (64 MiB or 4× the teaser cap)
  - `RLIMIT_NOFILE` 64
  - no core dumps
  - `no_new_privs`
- caps stdout (ffprobe JSON 1 MiB, still frames 32 MiB)
- runs at most `MaxParallel` children at once (default 2). The same number bounds in-process image decodes.

The wall-clock timeout is 60 s per call by default. When an allocation fails under `RLIMIT_AS`, ffmpeg 8.1 sometimes spins instead of exiting; the CPU and time limits end it.

Go cannot set rlimits between fork and exec. Instead, the runner starts its own executable with a private first argument. `media.MaybeRunLimited()` sees that argument, applies the limits and execs ffmpeg. **`snap-server`'s `main` must call `media.MaybeRunLimited()` before anything else.** Without it, the helper invocation would start a second server.

## Limits and tuning

`media.Config` fields, zero means default:

| Field | Default | Notes |
|---|---|---|
| `FFmpeg`, `FFprobe` | `ffmpeg`, `ffprobe` from `PATH` | |
| `Timeout` | 60 s | per ffmpeg/ffprobe call |
| `MemLimit` | 1 GiB | ffmpeg 8.1 needs about 200 MiB just to start. 4K video fits; 8K may not. |
| `CPULimit` | 120 s | summed over threads |
| `Threads` | 2 | |
| `MaxParallel` | 2 | |
| `MaxPixels` | 100,000,000 | a 100 MP RGBA decode needs about 400 MB |
| `ThumbWidth`, `PosterWidth` | 640 | |
| `TeaserWidth` | 480 | |
| `TeaserMaxBytes` | 1 MiB | |

The runtime image must use the same Alpine ffmpeg package as the dev image (8.1 with libx264, libdav1d and the native HEVC decoder). The tests depend on it.
