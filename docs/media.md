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

## How uploads are processed

`internal/mediajobs` runs these steps for file items (never pastes, links or secrets). The type always comes from `media.Sniff` on the stored bytes; ffmpeg only ever reads a file whose sniffed type has a `Container`.

**At upload, before the response** (the items `OnCreate` hook):

1. JPEG location stripping, if the owner's **Strip JPEG location** default is on. The stripped copy is written to a new blob; the item's blob, size and SHA-256 are swapped in one transaction and the owner's usage moves by the size difference (the stripped file is smaller). On any error the original stays as uploaded.
2. For images Go decodes (JPEG, PNG, GIF, WebP, BMP, TIFF): dimensions, duration of animations, and the thumbnail. The upload response's `thumbnail_url` (ShareX) therefore already points at `/{slug}/thumb`.
3. If ffmpeg has more to do, a `media.process` job is queued.

**The `media.process` job** (durable, 2 workers, the same bound as ffmpeg children; 5 minutes per job):

| Item | What the job stores |
|---|---|
| video | dimensions, duration, the poster as the thumbnail, the teaser |
| audio | duration |
| AVIF, HEIC | dimensions and the thumbnail |
| animated GIF, APNG, WebP | the teaser (animated WebP is decoded in Go; ffmpeg only encodes) |

Partial results are kept: a video whose teaser fails still gets its poster. Unsupported input, outputs over a limit and ffmpeg timeouts fail the job without retries; other errors retry up to three times. A job for an item deleted in the meantime does nothing, and blobs made for it are removed.

Thumbnails, posters and teasers are blobs referenced by `thumb_key` and `preview_key`. They are deleted with the item and when replaced, and do not count towards the owner's quota.

**Without ffmpeg** (`ffmpeg` or `ffprobe` not on `PATH` at startup): the server logs a warning, images still get Go thumbnails (through `media.NewImageRunner`, whose ffmpeg steps return `ErrUnsupported`), and nothing is queued for video, audio, AVIF, HEIC or teasers. Video share pages then have no poster.

Settings:

| Key | Default | Label | Notes |
|---|---|---|---|
| `media.teasers` | `true` | Video hover previews | Off: new uploads get no teaser; existing ones stay. |

Change it with `snap-server admin settings set media.teasers false`.

## Share pages

Video pages use the poster and load nothing until played (`preload="none"`); without a poster they load metadata only. The meta list shows dimensions and duration after the type, e.g. `mp4 · 1920×1080 · 1:02`.

**Embeds.** Share pages carry OpenGraph and Twitter tags so chat apps can build a card:

- Unlisted items without a password: `og:site_name` (the owner's **embed title**, else the site name), `og:title` (title or filename), `og:type` (`website`, or `video.other` with a video), `og:url`, and `twitter:card`.
  - Images: `og:image` is the raw file (JPEG, PNG, GIF, WebP) or the thumbnail (other formats), with `og:image:width`/`height`; `twitter:card` is `summary_large_image`.
  - Video: `og:image` is the poster. MP4 and WebM also get `og:video`, `og:video:secure_url` (https only), `og:video:type`, `og:video:width`/`height`, which Discord and Telegram play inline. `twitter:card` is `summary_large_image` (a player card needs an embeddable player page, which snap does not have).
  - `theme-color` is the owner's **embed color**, if set.
- Private items (seen by their owner), password-protected items (the unlock page, and the page after unlocking) and anything else get a generic card: the site name as `og:site_name` and `og:title`, and nothing about the item: no media, title, filename or URL. Expired and other people's private items are 404s.

URLs are absolute and use `SNAP_RAW_URL` for raw files and thumbnails when it is set. There are no oEmbed (`<link rel="alternate">`) links.

## Hover previews

`internal/web/static/js/hover-preview.js` plays a teaser over a poster while the mouse rests on it. Any page can use it by loading the module and following this markup:

```html
<div data-preview="/slug/preview">
  <span class="frame"><img src="/slug/thumb" alt=""></span> …
</div>
```

- `data-preview` is the teaser URL and marks the hover target.
- The target contains an `<img>`, the poster. The module puts `<video muted playsinline loop preload="auto">` right after it, in the image's parent, covering it; that parent gets `data-preview-frame` (made `position: relative` by app.css unless it is already positioned), so it should be the poster's size.
- Elements added later work too; events are delegated from `document`.

After 250 ms of a mouse pointer on the target, the teaser loads and fades in over 120 ms once it plays, with a thin `pb-progress` line along the bottom tracking playback. Leaving pauses and removes it, restoring the poster. One teaser plays at a time. Touch and pen pointers are ignored, so a tap opens the item as usual. Nothing happens under `prefers-reduced-motion`, with Save-Data, or when the teaser fails to load (the poster stays). The styles are in app.css under "hover teasers"; there are no inline styles. Only set `data-preview` when the item has a `preview_key`.

The composer's recent uploads use it, with small thumbnails and the opened badge (`{{template "item.opened" .Item}}`, from `_partials/item-badge.html`).
