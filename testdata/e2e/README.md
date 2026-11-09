# e2e test clip

`clip.ts` is the media the e2e tests (`src/e2e`, build tag `e2e`) publish into the SRT ingest. The tests push its bytes as they are; nothing in CI encodes, decodes or transcodes it.

It is synthetic test media generated once with ffmpeg 6.1.1, roughly matching the OBS settings the README recommends (H.264 with a 2s keyframe interval, AAC):

```sh
ffmpeg -f lavfi -i testsrc2=size=640x360:rate=30 \
  -f lavfi -i sine=frequency=440:sample_rate=48000 -t 60 \
  -c:v libx264 -profile:v main -pix_fmt yuv420p -g 60 -keyint_min 60 -sc_threshold 0 \
  -b:v 400k -maxrate 400k -bufsize 800k \
  -c:a aac -ac 2 -b:a 96k \
  -f mpegts clip.ts
```

- Video: H.264 Main, 640x360 at 30 fps, IDR every 2s, about 400 kbit/s
- Audio: AAC-LC stereo, 48 kHz, 440 Hz tone
- Duration: 60s, about 4 MB, long enough for the Windows player to see 10s of playback after the playlist primes
