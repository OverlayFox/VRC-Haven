// mfplayer plays a live HLS stream through Windows Media Foundation, which VRChat's AVPro player uses on PC,
// and reports whether it really plays: the manifest loads, video frames decode and the playback position advances.
//
// Usage: mfplayer <playlist url> [seconds of playback required, default 10] [timeout seconds, default 40]
// Exit codes: 0 played, 1 failed to play, 2 bad usage.

using System.Globalization;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.Media.Streaming.Adaptive;

internal static class Program
{
    private static async Task<int> Main(string[] args)
    {
        if (args.Length < 1 || !Uri.TryCreate(args[0], UriKind.Absolute, out var uri))
        {
            Console.Error.WriteLine("usage: mfplayer <playlist url> [required seconds] [timeout seconds]");
            return 2;
        }
        var required = TimeSpan.FromSeconds(args.Length > 1 ? double.Parse(args[1], CultureInfo.InvariantCulture) : 10);
        var timeout = TimeSpan.FromSeconds(args.Length > 2 ? double.Parse(args[2], CultureInfo.InvariantCulture) : 40);

        // The HLS source AVPro gets from Media Foundation, created explicitly so a manifest problem is reported precisely
        var created = await AdaptiveMediaSource.CreateFromUriAsync(uri);
        if (created.Status != AdaptiveMediaSourceCreationStatus.Success)
        {
            Fail($"Media Foundation rejected the playlist: {created.Status}, HTTP {created.HttpResponseMessage?.StatusCode}, {Hresult(created.ExtendedError)}");
            return 1;
        }
        var source = created.MediaSource;
        Log($"playlist accepted: live={source.IsLive}, bitrates=[{string.Join(", ", source.AvailableBitrates)}]");
        source.DownloadFailed += (_, e) =>
            Log($"download failed: {e.ResourceType} {e.ResourceUri}, HTTP {e.HttpResponseMessage?.StatusCode}, {Hresult(e.ExtendedError)}");

        using var player = new MediaPlayer
        {
            AutoPlay = true,
            IsMuted = true,
            // Frame server mode raises an event per decoded video frame, proving video decodes and not just the clock running
            IsVideoFrameServerEnabled = true,
        };
        long frames = 0;
        player.VideoFrameAvailable += (_, _) => Interlocked.Increment(ref frames);
        var failure = new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
        player.MediaFailed += (_, e) => failure.TrySetResult($"{e.Error}, {Hresult(e.ExtendedErrorCode)}, {e.ErrorMessage}");
        player.Source = MediaSource.CreateFromAdaptiveMediaSource(source);

        var session = player.PlaybackSession;
        var deadline = DateTime.UtcNow + timeout;
        TimeSpan? startPosition = null;
        var nextReport = DateTime.UtcNow;
        while (DateTime.UtcNow < deadline)
        {
            if (failure.Task.IsCompleted)
            {
                Fail($"Media Foundation failed to play: {failure.Task.Result}");
                return 1;
            }

            var state = session.PlaybackState;
            var position = session.Position;
            if (state == MediaPlaybackState.Playing && startPosition is null)
            {
                startPosition = position;
                Log($"playing from position {position}");
            }
            var played = startPosition is null ? TimeSpan.Zero : position - startPosition.Value;
            var decoded = Interlocked.Read(ref frames);

            if (DateTime.UtcNow >= nextReport)
            {
                // BufferingProgress is not read: the HLS source does not implement it and the getter throws InvalidCastException
                Log($"state={state} position={position} played={played.TotalSeconds:F1}s video={session.NaturalVideoWidth}x{session.NaturalVideoHeight} frames={decoded}");
                nextReport = DateTime.UtcNow.AddSeconds(2);
            }

            if (played >= required && session.NaturalVideoWidth > 0 && decoded > 0)
            {
                Log($"PASS: played {played.TotalSeconds:F1}s of {session.NaturalVideoWidth}x{session.NaturalVideoHeight} video, {decoded} frames decoded");
                return 0;
            }
            await Task.Delay(250);
        }

        Fail($"timed out after {timeout.TotalSeconds}s: state={session.PlaybackState} position={session.Position} " +
             $"video={session.NaturalVideoWidth}x{session.NaturalVideoHeight} frames={Interlocked.Read(ref frames)}");
        return 1;
    }

    private static string Hresult(Exception? e) => e is null ? "no extended error" : $"0x{e.HResult:X8} {e.Message.Trim()}";

    private static void Log(string message) => Console.WriteLine($"{DateTime.UtcNow:HH:mm:ss.fff} {message}");

    private static void Fail(string message) => Console.Error.WriteLine($"{DateTime.UtcNow:HH:mm:ss.fff} FAIL: {message}");
}
