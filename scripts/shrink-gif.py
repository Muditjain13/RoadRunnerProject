#!/usr/bin/env python3
"""Shrink a screen-recording GIF: resize, thin the frames, reduce the palette.

Playback speed is kept by adding the duration of every dropped frame to the
frame that is kept. Only needs Pillow (pip install pillow).

  python3 scripts/shrink-gif.py raw.gif docs/assets/demo-booking.gif
  python3 scripts/shrink-gif.py raw.gif out.gif --width 800 --keep-every 3

The defaults (960 px wide, every 2nd frame, 128 colors) took a 7 MB and a 15 MB
recording down to 1.6 MB and 3.4 MB and stayed readable. Look at the result
before committing it.
"""
import argparse
import os

from PIL import Image


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("src")
    p.add_argument("dst")
    p.add_argument("--width", type=int, default=960)
    p.add_argument("--keep-every", type=int, default=2, help="keep every Nth frame")
    p.add_argument("--colors", type=int, default=128)
    args = p.parse_args()

    im = Image.open(args.src)
    frames, durations, pending = [], [], 0
    last = im.n_frames - 1
    for i in range(im.n_frames):
        im.seek(i)
        pending += im.info.get("duration", 40)
        if i % args.keep_every == 0 or i == last:
            f = im.convert("RGB")
            f = f.resize((args.width, round(args.width * f.height / f.width)), Image.LANCZOS)
            f = f.quantize(colors=args.colors, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
            frames.append(f)
            durations.append(pending)
            pending = 0

    frames[0].save(
        args.dst, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True, disposal=1
    )
    mb = os.path.getsize(args.dst) / 1e6
    print(f"{args.dst}: {mb:.2f} MB, {len(frames)} frames, {sum(durations) / 1000:.1f} s")


if __name__ == "__main__":
    main()
