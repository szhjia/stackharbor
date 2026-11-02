# Local lifecycle patch

Based on github.com/muesli/cancelreader v0.2.2, with its MIT license retained.

On BSD/macOS and Linux, Bubble Tea can close its input reader while the input
goroutine is still returning from cancellation. Upstream Close frees the kernel
poller and pipe while Read still calls Fd on the cancellation pipe, causing a
data race and unsafe descriptor lifetime.

The BSD and Linux implementations now cancel and join an active Read before
releasing resources. New reads observe cancellation; concurrent Close calls
release resources once. The other platform implementations are unchanged. The
regression tests hold the reader inside descriptor dispatch and verify Close
waits for it, including concurrent Close calls on Linux.

Run the patched module tests from the repository root with:

    go test -race github.com/muesli/cancelreader

Remove the local replacement when an upstream release provides equivalent lifetime
synchronization. Do not patch the shared Go module cache.
