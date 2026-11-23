MediaPlayer wraps a plain `<video>` or `<audio>` in kit controls: play and pause, seek with buffered progress and a hover time, back and forward 10 s, current and total time, mute and volume, playback speed, captions, picture-in-picture and fullscreen.

- Consumer provides: `<figure class="pb-player" data-player aria-label="…">` holding a `<video src poster preload="metadata" playsinline>` (optional `<track kind="captions">`), or `pb-player--audio` with an `<audio>` and an optional `.pb-player__head` (icon, `.pb-player__title`, `.pb-player__meta`). Do not set `controls`; the script removes it.
- Script: `pbPlayer(figure)` (in `patchbay.js`, run by `pbInit()`). It builds the controls; buttons that the browser or file cannot support (captions without tracks, PiP, fullscreen in a sandbox) stay hidden.
- Layouts: default docks the controls under the picture on `surface`. `pb-player--overlay` floats them over the picture on a dark bar that hides after 2.5 s of playback without movement; fullscreen always uses the overlay. Inside a Media card use `.pb-media__frame--player`.
- Keyboard (focus anywhere in the player): Space or K play/pause, J / L back and forward 10 s, ← / → 5 s, ↑ / ↓ volume, M mute, C captions, F fullscreen, 0–9 jump to 0–90 %, < / > speed. Speed menu: ↑↓ and Enter.
- Seek colors: played in `accent` (docked) or `pop-mint` (overlay), buffered in `line-strong`, the rest `surface-sunk`.
- Always give the figure an `aria-label` naming the file. Offer a Download button next to the player for shared files.
- Video height is capped at `--pb-player-max-h` (default `80vh`); set it on the figure or a container, e.g. `data-vars="--pb-player-max-h:60vh"`.
- Until the media's duration is known (for example with `preload="none"`) the time reads `--:--`; give the figure `data-duration="252"` (seconds) or `data-duration="4:12"` to show the real length up front.
