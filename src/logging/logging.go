// Package logging builds the root logger shared by all components.
//
// Every line carries "node" (escort or flagship) and "component" (hls, srt, haven, geo), nested parts add "module" (buffer, demuxer).
// Lines about one HLS player carry "client_ip" (without port) and "viewer_id", so `grep viewer_id=<id>` shows that player's whole session.
package logging

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// timeFormat keeps the date and milliseconds, so lines from long runs can be ordered and matched to client reports.
const timeFormat = "2006-01-02T15:04:05.000Z07:00"

// New returns the root logger for node.
// LOG_LEVEL sets the level (trace, debug, info, warn, error, default info).
// LOG_FORMAT=json writes one JSON object per line for jq or log shippers, otherwise lines are console text, colored only on a terminal.
func New(node string, level zerolog.Level) zerolog.Logger {
	zerolog.TimeFieldFormat = timeFormat //nolint:reassign // zerolog only takes the timestamp format globally

	var out io.Writer = os.Stdout
	if !strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") {
		out = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: timeFormat, NoColor: !isTerminal(os.Stdout)}
	}
	logger := zerolog.New(out).With().Timestamp().Str("node", node).Logger()
	return logger.Level(level)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
